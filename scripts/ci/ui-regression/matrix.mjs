import { appendFileSync } from "node:fs";
import { inventory } from "../../../web/tests/ui-regression/matrix.mts";
const families = inventory.surfaces.map(surface => surface.id);
const matrix = JSON.stringify({ family: [
  ...families.filter(id => id === "public-archive-share"),
  ...families.filter(id => id !== "public-archive-share"),
] });
if (!process.env.GITHUB_OUTPUT) throw new Error("GitHub output path required");
appendFileSync(process.env.GITHUB_OUTPUT, "matrix=" + matrix + "\n");
