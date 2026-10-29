import type { NextConfig } from "next";

const apiUrl = process.env.WMS_API_URL ?? "http://localhost:8081";

const nextConfig: NextConfig = {
  agentRules: false,
  skipTrailingSlashRedirect: true,
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  async rewrites() {
    return [
      { source: "/api/:path*/", destination: `${apiUrl}/:path*/` },
      { source: "/api/:path*", destination: `${apiUrl}/:path*` },
    ];
  },
};

export default nextConfig;
