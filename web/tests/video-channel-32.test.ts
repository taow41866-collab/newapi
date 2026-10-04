import { expect, test } from "bun:test";

import { appendMissingVideoChannel32, channel32VideoChannel } from "../src/lib/default-video-channel";

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
