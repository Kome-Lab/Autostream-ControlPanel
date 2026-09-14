import { appendFileSync } from "node:fs";
import { inventory } from "../../../web/tests/ui-regression/matrix.mts";
const matrix = JSON.stringify({ family: inventory.surfaces.map(surface => surface.id) });
if (!process.env.GITHUB_OUTPUT) throw new Error("GitHub output path required");
appendFileSync(process.env.GITHUB_OUTPUT, "matrix=" + matrix + "\n");
