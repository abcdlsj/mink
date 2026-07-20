import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/sumi.system.v1.SystemService": "http://127.0.0.1:8080",
      "/sumi.agent.v1.AgentService": "http://127.0.0.1:8080",
      "/sumi.computer.v1.ComputerService": "http://127.0.0.1:8080",
      "/sumi.placement.v1.PlacementService": "http://127.0.0.1:8080",
      "/sumi.space.v1.CollaborationService": "http://127.0.0.1:8080",
      "/sumi.organization.v1.OrganizationService": "http://127.0.0.1:8080",
      "/sumi.grant.v1.GrantService": "http://127.0.0.1:8080",
      "/auth": "http://127.0.0.1:8080",
    },
  },
});
