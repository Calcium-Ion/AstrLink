import { describe, expect, it } from "vitest";
import { parseServiceProxy, proxyDraft, proxyInput, validProxyDraft, validProxyURL } from "./service-proxy-model";

describe("instance proxy configuration", () => {
  it("defaults to inheritance and keeps credentials write-only", () => {
    expect(proxyInput(proxyDraft())).toBeNull();
    const draft = proxyDraft({ mode:"custom", url:"https://proxy.example:8443", credential_ref:"local://service-proxy/service_one" });
    expect(proxyInput(draft)).toEqual({mode:"custom",url:"https://proxy.example:8443"});
    expect(proxyInput({...draft,removeCredential:true})).toEqual({mode:"custom",url:draft.url,credential:null});
    expect(proxyInput({...draft,mode:"direct"})).toEqual({mode:"direct"});
    expect(() => parseServiceProxy({mode:"custom",url:draft.url,password:"secret"},"service_one")).toThrow();
    expect(() => parseServiceProxy({mode:"custom",url:draft.url,credential_ref:"local://service-proxy/service_two"},"service_one")).toThrow();
  });
  it("validates proxy addresses and separate authentication", () => {
    for (const scheme of ["http","https","socks5"]) expect(validProxyURL(`${scheme}://[::1]:1080`)).toBe(true);
    for (const url of ["ftp://proxy","http://user:secret@proxy","http://proxy/path","http://proxy?x","http://proxy#x","http://proxy:0","http://proxy:"]) expect(validProxyURL(url)).toBe(false);
    expect(validProxyDraft({...proxyDraft({mode:"custom",url:"http://proxy"}),password:"secret"})).toBe(false);
    expect(validProxyDraft({...proxyDraft({mode:"custom",url:"http://proxy"}),username:"user",password:"secret"})).toBe(true);
  });
});
