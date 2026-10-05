import { expect, test } from "bun:test";

import { appendMissingVideoChannel32, channel32VideoChannel } from "../src/lib/default-video-channel";
import { buildSd25VideoRequest } from "../src/lib/sd25-video-request";

test("adds New API channel 32 as a selectable video channel without credentials", () => {
    expect(channel32VideoChannel).toMatchObject({
        id: "newapi-video-32",
        name: "Seedance 视频（渠道32）",
        baseUrl: "https://newapi.jijiucanvas.com",
        apiKey: "",
        apiFormat: "openai",
    });
    expect(channel32VideoChannel.models).toEqual([
        { name: "SD2.5满血满参兜底线路一", capability: "video" },
        { name: "SD2.5满血满参兜底线路二", capability: "video" },
        { name: "SD2.5特价10-10-10-线路二", capability: "video" },
        { name: "SD2.5特价30-10-10-线路一", capability: "video" },
        { name: "SD2.5特价30-10-10-线路三", capability: "video" },
        { name: "SD2.5特价900-线路四", capability: "video" },
    ]);
});

test("adds the default video channel when no matching channel exists", () => {
    const existing = {
        id: "custom",
        name: "自定义渠道",
        baseUrl: "https://other.example",
        apiKey: "user-key",
        apiFormat: "openai" as const,
        models: [{ name: "grok-imagine-video", capability: "video" as const }],
    };

    expect(appendMissingVideoChannel32([existing])).toEqual([existing, channel32VideoChannel]);
});

test("merges missing video models into an existing endpoint without replacing credentials", () => {
    const configuredChannel = {
        id: "my-newapi",
        name: "My New API",
        baseUrl: `${channel32VideoChannel.baseUrl}/v1`,
        apiKey: "user-key",
        apiFormat: "openai" as const,
        models: [channel32VideoChannel.models[0], { name: "custom-video", capability: "video" as const }],
    };

    const [merged] = appendMissingVideoChannel32([configuredChannel]);

    expect(merged.id).toBe("my-newapi");
    expect(merged.name).toBe("My New API");
    expect(merged.baseUrl).toBe(configuredChannel.baseUrl);
    expect(merged.apiKey).toBe("user-key");
    expect(merged.models).toEqual([...configuredChannel.models, ...channel32VideoChannel.models.slice(1)]);
});

test("builds the documented SD2.5 request with public frame URLs and fixed 720p", () => {
    expect(
        buildSd25VideoRequest({
            model: "SD2.5特价900-线路四",
            prompt: "a cat running",
            references: [
                { id: "first", name: "first.png", type: "image/png", dataUrl: "data:image/png;base64,AA==", url: "https://cdn.example/first.png" },
                { id: "last", name: "last.png", type: "image/png", dataUrl: "data:image/png;base64,AA==", url: "https://cdn.example/last.png" },
            ],
            mode: "frames",
            seconds: "25",
            aspectRatio: "16:9",
            watermark: false,
        }),
    ).toEqual({
        model: "SD2.5特价900-线路四",
        prompt: "a cat running",
        seconds: 25,
        resolution: "720p",
        aspect_ratio: "16:9",
        first_frame_image: "https://cdn.example/first.png",
        last_frame_image: "https://cdn.example/last.png",
        watermark: false,
    });
});

test("rejects local-only SD2.5 reference images", () => {
    expect(() =>
        buildSd25VideoRequest({
            model: "SD2.5特价900-线路四",
            prompt: "a cat running",
            references: [{ id: "local", name: "local.png", type: "image/png", dataUrl: "data:image/png;base64,AA==", url: "blob:local-image" }],
            mode: "frames",
            seconds: "8",
            aspectRatio: "16:9",
            watermark: false,
        }),
    ).toThrow("公网 HTTP(S) URL");
});

test("uses the documented image-reference array for reference mode", () => {
    expect(
        buildSd25VideoRequest({
            model: "SD2.5特价900-线路四",
            prompt: "a cat running",
            references: [{ id: "ref", name: "ref.png", type: "image/png", dataUrl: "", url: "https://cdn.example/ref.png" }],
            mode: "reference",
            seconds: "8",
            aspectRatio: "9:16",
            watermark: true,
        }),
    ).toEqual({
        model: "SD2.5特价900-线路四",
        prompt: "a cat running",
        seconds: 8,
        resolution: "720p",
        aspect_ratio: "9:16",
        watermark: true,
        images: ["https://cdn.example/ref.png"],
    });
});
