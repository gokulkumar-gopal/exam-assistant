import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Expose the MOCK_API flag to the browser bundle (it only toggles fixtures; not a secret).
  env: { NEXT_PUBLIC_MOCK_API: process.env.MOCK_API === "true" ? "true" : "false" },
  async rewrites() {
    // API_URL is server-only (no NEXT_PUBLIC_ prefix), no trailing slash.
    return [{ source: "/api/:path*", destination: `${process.env.API_URL}/:path*` }];
  },
};

export default nextConfig;
