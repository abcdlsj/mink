import "@fontsource-variable/space-grotesk";
import "@fontsource-variable/noto-sans-sc";
import "./styles.css";
import "./components/channel/channel.css";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { router } from "./router";

const queryClient = new QueryClient();

const root = document.getElementById("root");
if (!root) {
  throw new Error("Sumi root element is missing");
}

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
