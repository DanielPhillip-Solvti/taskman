# Selector map

Taken from a real `project.task` form-view page on the client-facing
ticketing Odoo instance (path-based routing, `/odoo/action-<id>/<project_id>`
style breadcrumbs — Odoo 17+). Confirmed against real markup pasted from
that instance; **not** confirmed against a second instance/theme, so treat
selector drift as likely if the ticketing Odoo's theme changes.

| Field | Selector | Notes |
|---|---|---|
| Task number | `div[name="id"] span` | Odoo's own readonly integer field for the record id. Reliable — this is Odoo core markup, not a custom view. |
| Project name | `.o_breadcrumb a[data-tooltip*="Back to"][data-tooltip*="form"]` | The breadcrumb link whose tooltip is `Back to "<project>" form`. More robust than an index-based `nth-child` pick since Odoo sometimes adds/removes leading ellipsis items. |
| Task title | `textarea#name_0` (`.value`), falling back to `.o_last_breadcrumb_item.active span.text-truncate` (`.textContent`) | The textarea holds the live editable value; the breadcrumb only reflects the last-saved title, used only if the textarea isn't found (defensive — Odoo sometimes suffixes the id, e.g. `name_1`, when multiple records are open in a multi-record form). |
| Description (HTML) | `div[name="description"] .odoo-editor-editable` (`.innerHTML`) | Odoo's rich-text editor body. |
| Chatter: Send message | `.o-mail-Chatter-sendMessage` | Opens the composer. |
| Chatter: Log note | `.o-mail-Chatter-logNote` | Opens the composer in "note" mode. |
| Chatter: file input | `.o-mail-Chatter-fileUploader` | Hidden `<input type=file multiple>`; a `DataTransfer` assigned here + a `change` event attaches files. |
| Composer text input | `.o-mail-Composer-input` (contenteditable or textarea, varies by Odoo version) | **Unvalidated** — the sample page didn't have an open composer captured, so this selector is inferred from Odoo's public `mail` module source, not observed directly. First write-back attempt should log what it actually finds and fail loudly (never silently) if this selector is wrong. |

## Known gaps (mirrors the original spec's §5 S3 spike, still open)

- No second ticketing-Odoo theme/version has been checked, so no confidence
  the project-name / title selectors generalize beyond this one instance.
- The composer's actual input selector is unconfirmed — write-back
  (`content.js`'s `openComposerAndDraft`) must be smoke-tested against a
  real ticket before being trusted for the "draft, never send" write-back
  behavior the whole approval-gate design depends on.
