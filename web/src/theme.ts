import { createTheme, type MantineColorsTuple } from "@mantine/core";

// Dark surface palette aligned with the app's existing CSS variables so Mantine
// components blend with the hand-rolled chrome during the migration.
//   bg #1a1d23, panel #23272f, border #333842, text #d0d0d0, muted #8a909c
const dark: MantineColorsTuple = [
  "#c9cdd3", // 0 - lightest text
  "#b8bdc6", // 1
  "#9aa0ab", // 2
  "#8a909c", // 3 - muted
  "#333842", // 4 - border
  "#2c313a", // 5 - hover surface
  "#23272f", // 6 - panel
  "#1a1d23", // 7 - body background
  "#15181d", // 8
  "#101216", // 9
];

// Brand blue approximating the app accent (#326ce5).
const brand: MantineColorsTuple = [
  "#e7f0ff",
  "#cfe0ff",
  "#9dbcff",
  "#6a97ff",
  "#4279f3",
  "#2a66ee",
  "#326ce5", // 6 - primary
  "#1f57c4",
  "#1349a8",
  "#003a8c",
];

export const theme = createTheme({
  primaryColor: "brand",
  primaryShade: 6,
  colors: { dark, brand },
  fontFamily:
    'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
});
