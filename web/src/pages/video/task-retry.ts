import type { VideoGenerationTaskState } from "@/services/api/video";

type RetryCoordinator = {
    query: () => Promise<VideoGenerationTaskState>;
    synchronize: (state: VideoGenerationTaskState) => Promise<void>;
    confirm: () => Promise<boolean>;
    create: () => Promise<void>;
};

export async function retryVideoTask(coordinator: RetryCoordinator): Promise<"synchronized" | "created"> {
    const state = await coordinator.query();
    await coordinator.synchronize(state);
    if (state.status !== "failed" || !state.retryable) return "synchronized";
    if (!(await coordinator.confirm())) return "synchronized";
    await coordinator.create();
    return "created";
}
