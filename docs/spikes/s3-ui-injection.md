# S3 — Odoo UI injection

**UNVALIDATED AGAINST A REAL INSTANCE.** No client-facing ticketing Odoo instance is reachable
from this sandbox. Everything below is derived from spec §10.2's assumptions about Odoo 17+'s
Owl-rendered form view and chatter, cross-checked against publicly-documented Odoo web-client
class names (`o_form_view`, `o_Chatter`, `o_Composer`, etc. — these are Odoo's own stable-ish
CSS class conventions, not invented), and exercised only against a locally-served fixture page:
`fixtures/ticket-pages/task-4821.html`. Every row below must be re-validated against a real
instance before Milestone 6/8 gates are trusted for production use — until then, treat this as
a starting selector map, not ground truth.

## Proposed selector map (spec §10.2), as data

```json
{
  "project_name": "[data-project-name]",
  "task_number": ".o_task_number .o_form_label",
  "title": ".o_field_widget.o_field_char[name='name'] .o_input",
  "description": ".o_field_widget.o_field_html[name='description'] .o_readonly",
  "description_append_marker": "[data-taskman-block]",
  "status_badge": ".o_statusbar_status .o_arrow_button.o_status",
  "chatter_composer_textarea": ".o_Composer_coreMain .o_ComposerTextInput_textarea",
  "chatter_file_input": ".o_Composer_coreMain .o_FileUploader_input",
  "chatter_send_button": ".o_Composer_actionButtons .o_Composer_buttonSend"
}
```

## Findings against the local fixture (real, but only proves the fixture, not Odoo)

- Every selector above resolves uniquely against `task-4821.html` when parsed with a standard
  DOM parser (jsdom-equivalent) — confirms the *selector syntax* is sound, not that Odoo's real
  DOM matches it.
- Setting `textarea.value` plus dispatching a native `input` event is the standard way to make a
  React/Owl-controlled input see an external write; the fixture has no framework attached to
  verify Owl's reactivity specifically survives this — **this is the single most important
  unvalidated claim in this spike** and must be checked against a real ticket page at M6/M8
  before relying on it. If Owl's reactivity does not pick up a raw `input` event, the fallback is
  Odoo's own `execCommand`-based contenteditable path or driving the composer via Odoo's RPC
  (`mail.thread.message_post`) instead of DOM injection — a materially different (and probably
  more robust) design for write-back that should be evaluated for real once a real instance is
  available.
- `DataTransfer`-based file attachment to a hidden `<input type=file>` is a documented working
  pattern in Chromium generally; untested here against Odoo's actual `FileUploader` widget
  behaviour (e.g. whether it also listens for `dragenter`/`drop` instead of `change`).
- Save/reload idempotency for `[data-taskman-block]` (spec §11.2) cannot be tested at all without
  a real Odoo backend to persist the HTML field and re-render it — the fixture is static and has
  no save path. Flagged as an M8 gate concern, not resolved here.

## Recommendation

Do not build Milestone 6/8 injection code against this selector map as if it were confirmed.
Budget the first real session against Daniel's actual ticketing instance as a manual selector
re-verification pass before writing the extraction/injection gates for real, and update this
file (and the JSON selector map) in place once that happens — do not create a second document.
