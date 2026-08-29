// Scraping is pure DOM reads, no network calls. See ../SELECTORS.md for the
// selector map and what's confirmed vs. inferred.

/**
 * @returns {{taskNumber:number, projectName:string} | null}
 * null means "this doesn't look like a task form page" — callers should not
 * render a degraded bar in that case, just stay silent.
 *
 * Title/description are deliberately NOT scraped here anymore: the daemon
 * fetches those (plus chatter and attachments) itself over Odoo's JSON-RPC
 * API using the session cookie, straight from the record — see
 * background.js's refineTask/implementTask handlers. That's both more
 * complete (chatter, attachments) and safer (the daemon controls exactly
 * how untrusted ticket text gets framed for the agent, instead of trusting
 * whatever this DOM scrape happened to pick up).
 */
function scrapeTaskPage() {
  const idEl = document.querySelector('div[name="id"] span');
  if (!idEl) return null; // not a task form view (or not yet rendered)

  const taskNumber = parseInt(idEl.textContent.trim(), 10);
  if (Number.isNaN(taskNumber)) return null;

  // Primary: the record's own project_id field, always present regardless
  // of breadcrumb style. The breadcrumb link (used first in an earlier
  // version of this scraper) turned out NOT to always exist — some ticket
  // URLs render a breadcrumb of just "Projects" / "Tasks" with no
  // project-name item at all, so it's now only a fallback.
  const projectFieldEl = document.querySelector('div[name="project_id"] a');
  const projectBreadcrumbEl = document.querySelector('.o_breadcrumb a[data-tooltip*="Back to"][data-tooltip*="form"]');
  const projectName = (projectFieldEl && projectFieldEl.textContent.trim())
    || (projectBreadcrumbEl && projectBreadcrumbEl.textContent.trim())
    || "";

  return { taskNumber, projectName };
}

// Exposed as a global (loaded as a plain content script, no bundler) so
// content.js can call it.
window.__taskmanScrape = scrapeTaskPage;
