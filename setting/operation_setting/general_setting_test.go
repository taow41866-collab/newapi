package operation_setting

import "testing"

func TestNormalizeDocsLink(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "retired production host", input: "https://hk.zhongzhuan.de5.net/docs/", want: DefaultDocsLink},
		{name: "retired host case insensitive", input: "HTTPS://HK.ZHONGZHUAN.DE5.NET/docs", want: DefaultDocsLink},
		{name: "custom host preserved", input: "https://docs.example.com/guide", want: "https://docs.example.com/guide"},
		{name: "empty value preserved", input: "  ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeDocsLink(tt.input); got != tt.want {
				t.Fatalf("NormalizeDocsLink(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
