const credentialKeys = ["baseUrl", "baseurl", "apiKey", "apikey"];

export function readChannelUrlBootstrap(search: string, hash: string) {
    const searchParams = new URLSearchParams(search);
    const hashParams = new URLSearchParams(hash.replace(/^#/, ""));
    const hasCredentials = (params: URLSearchParams) => credentialKeys.some((key) => params.has(key));
    const hasHashCredentials = hasCredentials(hashParams);
    if (!hasHashCredentials && !hasCredentials(searchParams)) return null;
    const source = hasHashCredentials ? hashParams : searchParams;
    const baseUrl = source.get("baseUrl") || source.get("baseurl");
    const apiKey = source.get("apiKey") || source.get("apikey");
    credentialKeys.forEach((key) => {
        searchParams.delete(key);
        hashParams.delete(key);
    });
    return {
        baseUrl,
        apiKey,
        remainingSearch: searchParams.size ? `?${searchParams}` : "",
        remainingHash: hasHashCredentials ? (hashParams.size ? `#${hashParams}` : "") : hash,
    };
}
