import type { ReferenceImage } from "@/types/image";

export function buildSd25VideoRequest({ model, prompt, references, mode, seconds, aspectRatio, watermark }: { model: string; prompt: string; references: ReferenceImage[]; mode: string; seconds: string; aspectRatio: string; watermark: boolean }) {
    const imageUrls = references.map((image) => {
        const value = image.url || image.dataUrl;
        try {
            const url = new URL(value);
            if (url.protocol === "https:" || url.protocol === "http:") return value;
        } catch {
            // Local browser data cannot be fetched by the upstream service.
        }
        throw new Error("该模型要求参考图为公网 HTTP(S) URL；当前图片仅在浏览器本地，不能发送。");
    });
    if (imageUrls.length > 9) throw new Error("该模型最多支持 9 张参考图。");

    const duration = Number(seconds);
    if (!Number.isFinite(duration) || duration < 4 || duration > 30) throw new Error("该模型时长必须在 4–30 秒之间。");
    const body: Record<string, unknown> = {
        model,
        prompt,
        seconds: duration,
        resolution: "720p",
        aspect_ratio: aspectRatio,
        watermark,
    };
    if (mode === "frames") {
        if (imageUrls[0]) body.first_frame_image = imageUrls[0];
        if (imageUrls[1]) body.last_frame_image = imageUrls[1];
    } else if (imageUrls.length) {
        body.images = imageUrls;
    }
    return body;
}
