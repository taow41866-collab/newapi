import type { ModelChannel } from "../stores/use-config-store";

export const channel32VideoChannel: ModelChannel = {
    id: "newapi-video-32",
    name: "Seedance 视频（渠道32）",
    baseUrl: "https://newapi.jijiucanvas.com",
    apiKey: "",
    apiFormat: "openai",
    models: [
        { name: "SD2.5满血满参兜底线路一", capability: "video" },
        { name: "SD2.5满血满参兜底线路二", capability: "video" },
        { name: "SD2.5特价10-10-10-线路二", capability: "video" },
        { name: "SD2.5特价30-10-10-线路一", capability: "video" },
        { name: "SD2.5特价30-10-10-线路三", capability: "video" },
        { name: "SD2.5特价900-线路四", capability: "video" },
    ],
};

function baseUrlKey(baseUrl: string) {
    try {
        const url = new URL(baseUrl.trim());
        url.hash = "";
        return url.toString().replace(/\/+$/, "").replace(/\/v1$/i, "");
    } catch {
        return baseUrl.trim().replace(/\/+$/, "").replace(/\/v1$/i, "");
    }
}

export function appendMissingVideoChannel32(channels: ModelChannel[]) {
    const channelById = channels.findIndex((channel) => channel.id === channel32VideoChannel.id);
    const channelByUrl = channels.findIndex((channel) => baseUrlKey(channel.baseUrl) === baseUrlKey(channel32VideoChannel.baseUrl));
    const existingIndex = channelById >= 0 ? channelById : channelByUrl;
    if (existingIndex >= 0) {
        const existing = channels[existingIndex];
        if (baseUrlKey(existing.baseUrl) !== baseUrlKey(channel32VideoChannel.baseUrl)) return channels;
        const existingModelNames = new Set(existing.models.map((model) => model.name));
        const missingModels = channel32VideoChannel.models.filter((model) => !existingModelNames.has(model.name));
        if (missingModels.length === 0) return channels;
        return channels.map((channel, index) => (index === existingIndex ? { ...channel, models: [...channel.models, ...missingModels.map((model) => ({ ...model }))] } : channel));
    }
    return [...channels, { ...channel32VideoChannel, models: channel32VideoChannel.models.map((model) => ({ ...model })) }];
}
