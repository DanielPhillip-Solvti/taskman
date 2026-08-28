// Scraping is pure DOM reads, no network calls. See ../SELECTORS.md for the
// selector map and what's confirmed vs. inferred.

/**
 * @returns {{taskNumber:number, projectName:string, title:string, descriptionHtml:string} | null}
 * null means "this doesn't look like a task form page" — callers should not
 * render a degraded bar in that case, just stay silent.
 */
function scrapeTaskPage() {
  const idEl = document.querySelector('div[name="id"] span');
  if (!idEl) return null; // not a task form view (or not yet rendered)

  const taskNumber = parseInt(idEl.textContent.trim(), 10);
  if (Number.isNaN(taskNumber)) return null;

  const projectEl = document.querySelector('.o_breadcrumb a[data-tooltip*="Back to"][data-tooltip*="form"]');
  const projectName = projectEl ? projectEl.textContent.trim() : "";

  const titleTextarea = document.querySelector('textarea[id^="name_"]');
  const titleBreadcrumb = document.querySelector('.o_last_breadcrumb_item.active span.text-truncate');
  const title = (titleTextarea && titleTextarea.value.trim())
    || (titleBreadcrumb && titleBreadcrumb.textContent.trim())
    || "";

  const descEl = document.querySelector('div[name="description"] .odoo-editor-editable');
  const descriptionHtml = descEl ? descEl.innerHTML : "";

  return { taskNumber, projectName, title, descriptionHtml };
}

// Exposed as a global (loaded as a plain content script, no bundler) so
// content.js can call it.
window.__taskmanScrape = scrapeTaskPage;
