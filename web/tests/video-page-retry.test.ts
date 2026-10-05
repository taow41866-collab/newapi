import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import ts from "typescript";
import React from "react";
import { retryVideoTask } from "../src/pages/video/task-retry";
import { normalizeVideoTaskStatus, shouldPollVideoTask } from "../src/lib/video-task-state";

const page = readFileSync(new URL("../src/pages/video/index.tsx", import.meta.url), "utf8");
const api = readFileSync(new URL("../src/services/api/video.ts", import.meta.url), "utf8");

// Run the page's actual closures with controlled side effects, without a DOM or paid upstream.
function loadFunction(source: string, name: string, environment: Record<string, unknown>): (...args: any[]) => any {
    const parsed = ts.createSourceFile("source.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    let implementation: string | undefined;
    const visit = (node: ts.Node) => {
        if (ts.isVariableDeclaration(node) && node.name.getText(parsed) === name && node.initializer) implementation = node.initializer.getText(parsed);
        if (ts.isFunctionDeclaration(node) && node.name?.text === name) implementation = node.getText(parsed);
        ts.forEachChild(node, visit);
    };
    visit(parsed);
    if (!implementation) throw new Error(`Missing source function: ${name}`);
    const emitted = ts.transpileModule(`const extracted = ${implementation};`, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None, jsx: ts.JsxEmit.React } }).outputText;
    return new Function(...Object.keys(environment), `${emitted}\nreturn extracted;`)(...Object.values(environment));
}

function fixture() {
    return {
        id: "original-log", task: { id: "original-task", model: "original-model", provider: "openai" },
        model: "original-model", status: "failed", createdAt: Date.now(),
        prompt: "original prompt", references: [{ id: "original-frame", url: "https://example.test/frame.png" }],
        config: { videoSeconds: "25", videoModel: "original-model" },
    };
}

function pageHarness(state: unknown) {
    const log = fixture();
    const controller = new AbortController();
    const saved: any[] = [];
    const submissions: any[] = [];
    let displayed: any[] = [];
    let confirmationCount = 0;
    const env: Record<string, any> = {
        previewLog: log, previewLogRef: { current: log }, retryingLogIdsRef: { current: new Set() },
        activeLogIdsRef: { current: new Set() }, pollAbortRef: { current: controller },
        effectiveConfig: { videoModel: "edited-model", videoSeconds: "8" }, model: "edited-model",
        buildVideoConfig: (config: any) => config,
        pollVideoGenerationTask: async () => { if (state instanceof Error) throw state; return state; },
        saveLog: async (value: any) => { saved.push(value); },
        setPreviewLog: (value: any) => { env.previewLog = value; env.previewLogRef.current = value; },
        setActivePreviewLog: (value: any) => { env.previewLog = value; env.previewLogRef.current = value; },
        setResults: (value: any) => { displayed = typeof value === "function" ? value(displayed) : value; },
        setRunning: () => {}, setStartedAt: () => {}, updateAgentTask: () => {},
        storeGeneratedVideo: async () => ({ url: "https://example.test/result.mp4", storageKey: "stored-video" }),
        isVideoTaskRetryable: (error: any) => error.retryable === true,
        nanoid: () => "result-id", t: (key: string) => key,
        message: { warning: () => {}, success: () => {}, error: () => {}, info: () => {} },
        Modal: { confirm: (options: any) => { confirmationCount++; options.onOk(); } },
        generate: async (snapshot: any) => { submissions.push(snapshot); },
        pollGenerationLog: async () => {}, retryVideoTask,
        delay: async () => { controller.abort(); },
    };
    return { log, env, saved, submissions, controller, get displayed() { return displayed; }, get confirmationCount() { return confirmationCount; } };
}

test("page rapid double-click queries, confirms and creates only once", async () => {
    const h = pageHarness({ status: "failed", retryable: true, error: "upstream failed" });
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    let queries = 0;
    h.env.pollVideoGenerationTask = async () => { queries++; await gate; return { status: "failed", retryable: true, error: "failed" }; };
    const retry = loadFunction(page, "retryResult", h.env);
    const first = retry();
    const second = retry();
    release();
    await Promise.all([first, second]);
    expect(queries).toBe(1);
    expect(h.confirmationCount).toBe(1);
    expect(h.submissions).toHaveLength(1);
});

test("page unknown upstream status synchronizes progress without confirmation or POST", async () => {
    const payload = { id: "original-task", status: "provider-new-status", progress: 42 };
    const query = loadFunction(api, "pollOpenAIVideoTask", {
        axios: { get: async () => ({ data: payload }) }, aiApiUrl: () => "https://example.test/videos/original-task",
        aiHeaders: () => ({}), unwrapVideoResponse: (value: any) => value,
        normalizeVideoTaskStatus, shouldPollVideoTask, videoResultUrl: () => undefined,
        apiText: (key: string) => key, readAxiosError: (error: Error) => error.message,
    });
    const h = pageHarness(null);
    h.env.pollVideoGenerationTask = () => query({}, h.log.task);
    await loadFunction(page, "retryResult", h.env)();
    expect(h.submissions).toHaveLength(0);
    expect(h.confirmationCount).toBe(0);
    expect(h.saved.at(-1)).toMatchObject({ status: "pending", progress: 42, providerStatus: "provider-new-status" });
});

test("page completion storage failure cannot confirm or POST a replacement", async () => {
    const h = pageHarness({ status: "completed", result: { url: "https://example.test/result.mp4" } });
    h.env.storeGeneratedVideo = async () => { throw new Error("storage full"); };
    await loadFunction(page, "retryResult", h.env)();
    expect(h.submissions).toHaveLength(0);
    expect(h.confirmationCount).toBe(0);
});

test("page original-task query errors cannot confirm or POST", async () => {
    const h = pageHarness(new Error("GET original HTTP 500"));
    await loadFunction(page, "retryResult", h.env)();
    expect(h.submissions).toHaveLength(0);
    expect(h.confirmationCount).toBe(0);
    expect(h.saved).toHaveLength(0);
});

test("page failure synchronization must persist before charge confirmation", async () => {
    const h = pageHarness({ status: "failed", retryable: true, error: "upstream failed" });
    h.env.saveLog = async () => { throw new Error("log storage unavailable"); };
    await loadFunction(page, "retryResult", h.env)();
    expect(h.submissions).toHaveLength(0);
    expect(h.confirmationCount).toBe(0);
});

test("page result list includes progress data in the pending result state", () => {
    expect(page).toContain('typeof log.progress === "number"');
    expect(page).toContain("Math.round(log.progress)");
});

test("page paid retry uses the original prompt, references and model snapshot", async () => {
    const h = pageHarness({ status: "failed", retryable: true, error: "upstream failed" });
    await loadFunction(page, "retryResult", h.env)();
    expect(h.submissions).toHaveLength(1);
    expect(h.submissions[0]).toMatchObject({
        text: h.log.prompt,
        config: h.log.config,
        references: h.log.references,
    });
});

test("older task polling cannot overwrite a different task preview", async () => {
    const h = pageHarness({ status: "completed", result: { url: "https://example.test/result.mp4" } });
    const selected = { ...fixture(), id: "selected-log", task: { ...fixture().task, id: "selected-task" } };
    h.env.previewLog = selected;
    h.env.previewLogRef.current = selected;
    h.env.setResults([{ id: "selected-result", status: "success", video: { id: "selected-video" } }]);
    await loadFunction(page, "pollGenerationLog", h.env)(h.log);
    expect(h.saved.at(-1)).toMatchObject({ id: h.log.id, status: "success" });
    expect(h.displayed).toEqual([{ id: "selected-result", status: "success", video: { id: "selected-video" } }]);
});

for (const state of [{ status: "completed", result: { url: "https://example.test/result.mp4" } }, { status: "pending", progress: 42 }, { status: "failed", retryable: false, error: "failed" }]) {
    test(`manual query ${state.status} cannot overwrite another selected task`, async () => {
        const h = pageHarness(state);
        const selected = { ...fixture(), id: "selected-log" };
        h.env.pollVideoGenerationTask = async () => {
            h.env.previewLogRef.current = selected;
            return state;
        };
        h.env.setResults([{ id: "selected-result", status: "pending" }]);
        await loadFunction(page, "retryResult", h.env)();
        expect(h.saved.at(-1)?.id).toBe(h.log.id);
        expect(h.env.previewLogRef.current).toBe(selected);
        expect(h.displayed).toEqual([{ id: "selected-result", status: "pending" }]);
        expect(h.submissions).toHaveLength(0);
    });
}

test("paid retry keeps the original snapshot contract visible in the page source", () => {
    expect(page).toContain("log.prompt");
    expect(page).toContain("log.references");
    expect(page).toContain("log.config");
    expect(page).toContain("create: async () => generate({");
    expect(page).toContain("text: log.prompt");
    expect(page).toContain("references: log.references");
});

test("each polling worker guards result updates by its own preview log id", () => {
    expect(page).toContain("previewLogRef.current?.id === log.id");
});

test("unmount aborts polling and prevents stale continuation", () => {
    expect(page).toContain("return () => pollAbortRef.current.abort()");
    expect(page).toContain("if (signal.aborted) return");
});
