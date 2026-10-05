export type VideoTaskStatus = "queued" | "in_progress" | "completed" | "failed" | "cancelled" | "unknown";

export function normalizeVideoTaskStatus(value: unknown): VideoTaskStatus {
    if (typeof value !== "string") return "unknown";
    const status = value.toLowerCase();
    if (["queued", "pending", "in_progress", "running", "processing"].includes(status)) return status === "pending" ? "queued" : status === "running" || status === "processing" ? "in_progress" : status as "queued" | "in_progress";
    if (status === "completed" || status === "failed" || status === "cancelled") return status;
    return "unknown";
}

export function shouldPollVideoTask(status: VideoTaskStatus) {
    return status !== "completed" && status !== "failed" && status !== "cancelled";
}
