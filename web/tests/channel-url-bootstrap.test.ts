import { expect, test } from "bun:test";

import { readChannelUrlBootstrap } from "../src/lib/channel-url-bootstrap";

test("imports credentials from the fragment without putting them in the query", () => {
    expect(readChannelUrlBootstrap("?view=canvas", "#baseUrl=https%3A%2F%2Flpss.online&apiKey=sk-test&agentUrl=localhost")).toEqual({
        baseUrl: "https://lpss.online",
        apiKey: "sk-test",
        remainingSearch: "?view=canvas",
        remainingHash: "#agentUrl=localhost",
    });
});

test("retains legacy query import and removes credential aliases", () => {
    expect(readChannelUrlBootstrap("?baseurl=https%3A%2F%2Flpss.online&apikey=sk-test&view=canvas", "#section")).toEqual({
        baseUrl: "https://lpss.online",
        apiKey: "sk-test",
        remainingSearch: "?view=canvas",
        remainingHash: "#section",
    });
});

test("prefers fragment credentials and clears credentials from both sources", () => {
    expect(readChannelUrlBootstrap("?baseUrl=https%3A%2F%2Fother.example&apiKey=sk-other", "#baseurl=https%3A%2F%2Flpss.online&apikey=sk-test")).toEqual({
        baseUrl: "https://lpss.online",
        apiKey: "sk-test",
        remainingSearch: "",
        remainingHash: "",
    });
});

test("does not combine a fragment key with an unrelated query host", () => {
    expect(readChannelUrlBootstrap("?baseUrl=https%3A%2F%2Fother.example", "#apiKey=sk-test")?.baseUrl).toBeNull();
});

test("returns null when there are no channel credentials", () => {
    expect(readChannelUrlBootstrap("?view=canvas", "#agentUrl=localhost&agentToken=test")).toBeNull();
});

test("removes empty credential parameters as well", () => {
    expect(readChannelUrlBootstrap("", "#apiKey=")).toEqual({ baseUrl: null, apiKey: null, remainingSearch: "", remainingHash: "" });
});
