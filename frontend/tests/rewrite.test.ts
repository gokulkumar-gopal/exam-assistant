import { afterEach, expect, it, vi } from "vitest";

afterEach(() => vi.unstubAllEnvs());

async function rewrites() {
  vi.resetModules();
  const { default: config } = await import("../next.config");
  return config.rewrites!();
}

it("rewrites /api/:path* to API_URL/:path* (strips /api)", async () => {
  vi.stubEnv("API_URL", "http://localhost:8080");
  expect(await rewrites()).toEqual([{ source: "/api/:path*", destination: "http://localhost:8080/:path*" }]);
});

it("emits no rewrite when API_URL is unset (mock mode / build without backend)", async () => {
  vi.stubEnv("API_URL", "");
  expect(await rewrites()).toEqual([]);
});
