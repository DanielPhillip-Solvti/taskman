// Executable gate for the scraper: loads the *real* extension source
// (src/scrape.js, unmodified) into a jsdom document built from a real
// captured ticket page, and asserts the extracted fields are correct. No
// mocking of the scraping logic itself — only the DOM it runs against is a
// fixture.
const fs = require("fs");
const path = require("path");
const { JSDOM } = require("jsdom");

const fixturePath = path.join(__dirname, "..", "fixtures", "task-13823.html");
const scrapeSrc = fs.readFileSync(path.join(__dirname, "..", "src", "scrape.js"), "utf8");

const dom = new JSDOM(fs.readFileSync(fixturePath, "utf8"), { runScripts: "outside-only" });
dom.window.eval(scrapeSrc);

const result = dom.window.__taskmanScrape();

let failures = 0;
function assertEqual(actual, expected, label) {
  if (actual !== expected) {
    failures++;
    console.error(`FAIL ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  } else {
    console.log(`ok   ${label}`);
  }
}

if (!result) {
  console.error("FAIL: scrapeTaskPage() returned null against a real task page fixture");
  process.exit(1);
}

assertEqual(result.taskNumber, 13823, "taskNumber");
assertEqual(result.projectName, "R&D / Solvti Hosting", "projectName");
assertEqual(result.title, "Reset Environments", "title");
assertEqual(
  result.descriptionHtml.includes("Ensure the 'main'/production branch is known on the VM."),
  true,
  "descriptionHtml contains first bullet"
);

// Negative case: a page with none of the task-form markup should scrape to null.
const blankDom = new JSDOM("<!doctype html><html><body></body></html>", { runScripts: "outside-only" });
blankDom.window.eval(scrapeSrc);
assertEqual(blankDom.window.__taskmanScrape(), null, "non-task page scrapes to null");

if (failures > 0) {
  console.error(`\n${failures} assertion(s) failed`);
  process.exit(1);
}
console.log("\nall scrape assertions passed");
