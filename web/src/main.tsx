import React from "react";
import ReactDOM from "react-dom/client";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import { SettingsProvider } from "./settings";
import { theme } from "./theme";
// Mantine styles first so our own styles.css can override where needed.
import "@mantine/core/styles.css";
import "./styles.css";

const queryClient = new QueryClient();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="dark">
      <QueryClientProvider client={queryClient}>
        <SettingsProvider>
          <App />
        </SettingsProvider>
      </QueryClientProvider>
    </MantineProvider>
  </React.StrictMode>,
);
