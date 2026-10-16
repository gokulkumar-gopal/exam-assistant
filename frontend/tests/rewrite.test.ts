import { expect, it } from "vitest";

it("rewrites /api/:path* to API_URL/:path* (strips /api)", async () => {
  process.env.API_URL = "https://api.example.com";
  const { default: config } = await import("../next.config");
  expect(await config.rewrites!()).toEqual([
    { source: "/api/:path*", destination: "https://api.example.com/:path*" },
  ]);
});
