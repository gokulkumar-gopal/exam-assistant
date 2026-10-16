import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Expose the MOCK_API flag to the browser bundle (it only toggles fixtures; not a secret).
  env: { NEXT_PUBLIC_MOCK_API: process.env.MOCK_API === "true" ? "true" : "false" },
  async rewrites() {
    // API_URL is server-only (no NEXT_PUBLIC_ prefix), no trailing slash.
    // Unset (mock mode, or a build without a backend): no rewrite, since
    // "undefined/:path*" is rejected by Next at startup.
    const apiUrl = process.env.API_URL;
    if (!apiUrl) return [];
    return [{ source: "/api/:path*", destination: `${apiUrl}/:path*` }];
  },
};

export default nextConfig;
