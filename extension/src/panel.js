// Turns a raw task log (as produced by the daemon — see internal/work's
// claudeStreamWriter) into UI-friendly pieces: a classified line-by-line
// "agent activity" stream, and — once the agent's final answer is in — a
// best-effort split of that answer into labeled sections (description,
// acceptance criteria, open questions, ...). Pure functions, no DOM/chrome
// access, so they're easy to reason about independent of content.js's
// rendering code.

/**
 * @param {string} line one line from the task log
 * @returns {"tool-call"|"tool-result"|"divider"|"error"|"text"}
 */
function classifyLogLine(line) {
  if (line.startsWith("→ ")) return "tool-call";
  if (line.startsWith("  ↳")) return "tool-result";
  if (/^--- task #\d+ (failed|interrupted)/.test(line)) return "error";
  if (line.startsWith("---")) return "divider";
  return "text";
}

/**
 * Pulls the agent's final prose answer out of the raw log: everything
 * after the last tool-call/tool-result line, with our own "---" framing
 * markers stripped. This is a heuristic (the log has no explicit
 * "final answer starts here" marker) but holds up well because the
 * refine/implement prompts explicitly ask the agent to print its findings
 * as one plain-markdown block at the end, after it's done investigating.
 *
 * @param {string} logText
 * @returns {string}
 */
function extractFinalAnswer(logText) {
  const lines = logText.split("\n");
  let lastToolIdx = -1;
  lines.forEach((line, i) => {
    const kind = classifyLogLine(line);
    if (kind === "tool-call" || kind === "tool-result") lastToolIdx = i;
  });
  return lines
    .slice(lastToolIdx + 1)
    .filter((line) => classifyLogLine(line) !== "divider" && classifyLogLine(line) !== "error")
    .join("\n")
    .trim();
}

const SECTION_KEYWORDS = [
  { kind: "description", re: /spec|description|summary|overview/i },
  { kind: "acceptance", re: /acceptance|criteria/i },
  { kind: "questions", re: /question|clarif/i },
];

function classifySectionTitle(title) {
  const hit = SECTION_KEYWORDS.find((k) => k.re.test(title));
  return hit ? hit.kind : "other";
}

/**
 * Splits the agent's final answer into {title, body, kind} sections on
 * markdown ATX headers (#/##/###). Falls back to one unlabeled section
 * when the answer doesn't use headers, so nothing is ever silently
 * dropped just because the agent formatted its answer differently.
 *
 * @param {string} text
 * @returns {Array<{title:string, body:string, kind:string}>}
 */
function splitAnswerSections(text) {
  if (!text) return [];
  const headerRe = /^#{1,4}\s+(.+)$/gm;
  const matches = [...text.matchAll(headerRe)];
  if (matches.length < 1) {
    return [{ title: "Agent findings", body: text, kind: "other" }];
  }
  const sections = [];
  for (let i = 0; i < matches.length; i++) {
    const start = matches[i].index + matches[i][0].length;
    const end = i + 1 < matches.length ? matches[i + 1].index : text.length;
    const title = matches[i][1].trim();
    const body = text.slice(start, end).trim();
    if (!body) continue;
    sections.push({ title, body, kind: classifySectionTitle(title) });
  }
  return sections;
}

// Exposed as globals (plain content scripts, no bundler/module system) so
// content.js can use them directly.
window.__taskmanPanel = { classifyLogLine, extractFinalAnswer, splitAnswerSections };
