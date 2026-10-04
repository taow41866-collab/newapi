package plugins_test

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/require"
)

func TestSoraResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "sora",
		model:     "sora-2-pro",
		requestBody: map[string]any{
			"model":   "sora-2-pro",
			"input":   "waves at sunset",
			"seconds": 8,
			"size":    "1792x1024",
		},
		wantAction: "text_to_video",
		wantRequest: map[string]any{
			"model":   "sora-2-pro",
			"prompt":  "waves at sunset",
			"seconds": float64(8),
			"size":    "1792x1024",
		},
		wantUsageKeys:  []string{"seconds", "size"},
		wantVendorName: "sora",
	})
}

func TestSoraSupportsSd25OpenAIVideoOnNewAPIUpstream(t *testing.T) {
	const model = "SD2.5特价900-线路四"
	source, err := builtinplugins.Source("sora")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "sora"})
	require.NoError(t, err)
	require.True(t, plugin.Meta.SupportsUpstream(jsplugin.UpstreamKindNewAPI))
	endpoint, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", model)
	require.True(t, found)
	require.Same(t, plugin, endpoint.Plugin)

	value, err := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
		"model": model,
		"body": map[string]any{"kind": "json", "value": map[string]any{
			"model": model, "prompt": "a cat running", "seconds": 25, "resolution": "720p",
			"first_frame_image": "https://cdn.example/first.png", "last_frame_image": "https://cdn.example/last.png",
		}},
	})
	require.NoError(t, err)
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	require.Equal(t, "image_to_video", decoded["action"])
	requestBody, ok := decoded["requestBody"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, model, requestBody["model"])
	require.Equal(t, "720p", requestBody["resolution"])
	require.Equal(t, "https://cdn.example/first.png", requestBody["first_frame_image"])

	value, err = plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
		"model": model,
		"body":  map[string]any{"kind": "json", "value": map[string]any{"model": model, "prompt": "a cat running", "images": []string{"https://cdn.example/ref.png"}}},
	})
	require.NoError(t, err)
	encoded, err = common.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	require.Equal(t, "image_to_video", decoded["action"])

	value, err = plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
		"model": model,
		"body":  map[string]any{"kind": "json", "value": map[string]any{"model": model, "prompt": "a cat running", "images": []string{}}},
	})
	require.NoError(t, err)
	encoded, err = common.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(encoded, &decoded))
	require.Equal(t, "text_to_video", decoded["action"])
}
