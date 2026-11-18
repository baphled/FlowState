import { execFileSync } from "node:child_process";
import { checkDocuments } from "./docs_contract.mjs";

const snapshot = JSON.parse(execFileSync("python3", ["-B", "scripts/doc_sources.py", ...process.argv.slice(2)], { encoding: "utf8" }));
const config = JSON.parse(execFileSync("git", ["show", "HEAD:.markdownlint.json"], { encoding: "utf8" }));
const errors = await checkDocuments(snapshot, config);
for (const error of errors) console.error(error);
if (errors.length) process.exitCode = 1;
else console.log(`PASS: changed Markdown structure and all ${Object.keys(snapshot.documents).length} committed documents' local links`);
