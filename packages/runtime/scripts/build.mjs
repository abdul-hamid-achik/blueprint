import { execFileSync } from "node:child_process";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

const packageRoot = fileURLToPath(new URL("../", import.meta.url));
const tsc = fileURLToPath(
  new URL("../node_modules/typescript/bin/tsc", import.meta.url),
);

await rm(new URL("../dist", import.meta.url), { recursive: true, force: true });

for (const config of ["tsconfig.json", "tsconfig.cjs.json"]) {
  execFileSync(process.execPath, [tsc, "-p", config], {
    cwd: packageRoot,
    stdio: "inherit",
  });
}

const cjsDirectory = new URL("../dist/cjs/", import.meta.url);
await mkdir(cjsDirectory, { recursive: true });
await writeFile(
  new URL("package.json", cjsDirectory),
  JSON.stringify({ type: "commonjs" }, null, 2) + "\n",
);
