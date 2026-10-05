import { expect, test } from "bun:test";
import { retryVideoTask } from "../src/pages/video/task-retry";
import type { VideoGenerationTaskState } from "../src/services/api/video";

function harness(state: VideoGenerationTaskState | Error, approved = true) {
    const events: string[] = [];
    const synchronized: VideoGenerationTaskState[] = [];
    const run = () => retryVideoTask({
        query: async () => { events.push("GET original"); if (state instanceof Error) throw state; return state; },
        synchronize: async (value) => { synchronized.push(value); events.push(`sync ${value.status}`); },
        confirm: async () => { events.push("confirm charge"); return approved; },
        create: async () => { events.push("POST new"); },
    });
    return { run, events, synchronized };
}

test("old client failure with running upstream only synchronizes the original task", async () => {
    const h = harness({ status: "pending", progress: 50, providerStatus: "in_progress" });
    expect(await h.run()).toBe("synchronized");
    expect(h.events).toEqual(["GET original", "sync pending"]);
});

test("completed task displays the existing result without a second creation", async () => {
    const h = harness({ status: "completed", result: { url: "https://example.test/video.mp4" } });
    expect(await h.run()).toBe("synchronized");
    expect(h.events).toEqual(["GET original", "sync completed"]);
});

test("confirmed upstream failure requires explicit confirmation before creating once", async () => {
    const h = harness({ status: "failed", retryable: true, error: "upstream failed" });
    expect(await h.run()).toBe("created");
    expect(h.events).toEqual(["GET original", "sync failed", "confirm charge", "POST new"]);
});

test("declining the new charge does not create a new task", async () => {
    const h = harness({ status: "failed", retryable: true, error: "cancelled" }, false);
    expect(await h.run()).toBe("synchronized");
    expect(h.events).toEqual(["GET original", "sync failed", "confirm charge"]);
});

for (const failure of ["timeout", "network error", "HTTP 404", "HTTP 401", "HTTP 429", "HTTP 500"]) {
    test(`${failure} during the original query never creates or confirms a new task`, async () => {
        const h = harness(new Error(failure));
        await expect(h.run()).rejects.toThrow(failure);
        expect(h.events).toEqual(["GET original"]);
    });
}

test("missing plugin result or unavailable output is not evidence of generation failure", async () => {
    const h = harness({ status: "failed", retryable: false, error: "result unavailable" });
    expect(await h.run()).toBe("synchronized");
    expect(h.events).toEqual(["GET original", "sync failed"]);
});

test("failure to save or display the original state cannot trigger paid resubmission", async () => {
    let created = 0;
    await expect(retryVideoTask({
        query: async () => ({ status: "failed", retryable: true, error: "failed" }),
        synchronize: async () => { throw new Error("local storage unavailable"); },
        confirm: async () => true,
        create: async () => { created++; },
    })).rejects.toThrow("local storage unavailable");
    expect(created).toBe(0);
});

test("pending synchronization preserves provider progress before continuing polling", async () => {
    const h = harness({ status: "pending", progress: 73, providerStatus: "processing" });

    expect(await h.run()).toBe("synchronized");
    expect(h.synchronized).toEqual([{ status: "pending", progress: 73, providerStatus: "processing" }]);
    expect(h.events).not.toContain("confirm charge");
    expect(h.events).not.toContain("POST new");
});

