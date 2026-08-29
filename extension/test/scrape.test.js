// Executable gate for the scraper: loads the *real* extension source
// (src/scrape.js, unmodified) into a jsdom document built from real
// captured ticket pages, and asserts the extracted fields are correct. No
// mocking of the scraping logic itself — only the DOM it runs against is a
// fixture.
const fs = require("fs");
const path = require("path");
const { JSDOM } = require("jsdom");

const fixturesDir = path.join(__dirname, "..", "fixtures");
const scrapeSrc = fs.readFileSync(path.join(__dirname, "..", "src", "scrape.js"), "utf8");

let failures = 0;
function assertEqual(actual, expected, label) {
  if (actual !== expected) {
    failures++;
    console.error(`FAIL ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  } else {
    console.log(`ok   ${label}`);
  }
}

function scrapeFixture(name) {
  const dom = new JSDOM(fs.readFileSync(path.join(fixturesDir, name), "utf8"), { runScripts: "outside-only" });
  dom.window.eval(scrapeSrc);
  return dom.window.__taskmanScrape();
}

// Fixture 1: task-13823.html — breadcrumb has a "Back to \"<project>\" form"
// item (older/richer breadcrumb style).
{
  const result = scrapeFixture("task-13823.html");
  if (!result) {
    console.error("FAIL: scrapeTaskPage() returned null against task-13823.html");
    process.exit(1);
  }
  assertEqual(result.taskNumber, 13823, "task-13823: taskNumber");
  assertEqual(result.projectName, "R&D / Solvti Hosting", "task-13823: projectName");
  assertEqual(result.title, "Reset Environments", "task-13823: title");
  assertEqual(
    result.descriptionHtml.includes("Ensure the 'main'/production branch is known on the VM."),
    true,
    "task-13823: descriptionHtml contains first bullet"
  );
}

// Fixture 2: task-14619.html — a real page whose breadcrumb is just
// "Projects" / "Tasks", with NO project-name breadcrumb item at all. This
// is the case that broke the original (breadcrumb-only) selector — the
// project name must come from the project_id field instead.
{
  const result = scrapeFixture("task-14619.html");
  if (!result) {
    console.error("FAIL: scrapeTaskPage() returned null against task-14619.html");
    process.exit(1);
  }
  assertEqual(result.taskNumber, 14619, "task-14619: taskNumber");
  assertEqual(result.projectName, "S00775 - Peter Pat", "task-14619: projectName (via project_id field, no breadcrumb item)");
  assertEqual(result.title, "Spotkanie odnośnie Magazynu i produkcji", "task-14619: title");
}

// Negative case: a page with none of the task-form markup should scrape to null.
{
  const blankDom = new JSDOM("<!doctype html><html><body></body></html>", { runScripts: "outside-only" });
  blankDom.window.eval(scrapeSrc);
  assertEqual(blankDom.window.__taskmanScrape(), null, "non-task page scrapes to null");
}

if (failures > 0) {
  console.error(`\n${failures} assertion(s) failed`);
  process.exit(1);
}
console.log("\nall scrape assertions passed");
