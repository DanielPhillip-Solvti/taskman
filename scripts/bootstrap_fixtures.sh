#!/usr/bin/env bash
# Creates fixtures/repos/17.0/{odoo,enterprise,demo_client} per
# implementation-brief.md §4.4. Idempotent — safe to re-run.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIX="$ROOT/fixtures/repos/17.0"
mkdir -p "$FIX"

# --- odoo: real shallow clone, cached -------------------------------------
if [ ! -d "$FIX/odoo/.git" ]; then
  echo "[bootstrap] cloning odoo 17.0 (shallow)..."
  git clone --depth 1 --branch 17.0 https://github.com/odoo/odoo.git "$FIX/odoo"
else
  echo "[bootstrap] odoo/ already present, skipping clone"
fi

# --- enterprise: stub, not the real (private) repo ------------------------
mkdir -p "$FIX/enterprise/account_accountant" "$FIX/enterprise/documents"
for mod in account_accountant documents; do
  cat > "$FIX/enterprise/$mod/__manifest__.py" <<EOF
{"name": "$mod (fixture stub)", "version": "17.0.1.0.0", "depends": ["base"], "license": "OEEL-1"}
EOF
  touch "$FIX/enterprise/$mod/__init__.py"
done

# --- demo_client: a real, minimal Odoo addon ------------------------------
DEMO="$FIX/demo_client"
if [ ! -d "$DEMO/.git" ]; then
  echo "[bootstrap] creating demo_client fixture repo..."
  mkdir -p "$DEMO/addons/taskman_demo/models" "$DEMO/addons/taskman_demo/views" "$DEMO/addons/taskman_demo/tests"
  cat > "$DEMO/addons/taskman_demo/__manifest__.py" <<'EOF'
{
    "name": "Taskman Demo",
    "version": "17.0.1.0.0",
    "depends": ["base"],
    "data": ["views/demo_widget_views.xml"],
    "license": "LGPL-3",
}
EOF
  cat > "$DEMO/addons/taskman_demo/__init__.py" <<'EOF'
from . import models
EOF
  cat > "$DEMO/addons/taskman_demo/models/__init__.py" <<'EOF'
from . import demo_widget
EOF
  cat > "$DEMO/addons/taskman_demo/models/demo_widget.py" <<'EOF'
from odoo import fields, models


class DemoWidget(models.Model):
    _name = "taskman.demo.widget"
    _description = "Taskman fixture model — exercises a real -u cycle"

    name = fields.Char(required=True)
    active = fields.Boolean(default=True)
EOF
  cat > "$DEMO/addons/taskman_demo/views/demo_widget_views.xml" <<'EOF'
<odoo>
  <record id="view_demo_widget_tree" model="ir.ui.view">
    <field name="name">taskman.demo.widget.tree</field>
    <field name="model">taskman.demo.widget</field>
    <field name="arch" type="xml">
      <tree>
        <field name="name"/>
      </tree>
    </field>
  </record>
</odoo>
EOF
  cat > "$DEMO/addons/taskman_demo/tests/__init__.py" <<'EOF'
from . import test_demo_widget
EOF
  cat > "$DEMO/addons/taskman_demo/tests/test_demo_widget.py" <<'EOF'
from odoo.tests.common import TransactionCase


class TestDemoWidget(TransactionCase):
    def test_create(self):
        widget = self.env["taskman.demo.widget"].create({"name": "test"})
        self.assertTrue(widget.active)
EOF
  cat > "$DEMO/README.md" <<'EOF'
# demo_client fixture

A minimal, real Odoo addon used by Taskman's integration gates. `main` is a working module;
the `broken` branch introduces a deliberate XML ParseError for failure-path testing
(implementation-brief.md §4.4).
EOF
  cat > "$DEMO/.gitignore" <<'EOF'
.context/tasks/
__pycache__/
*.pyc
EOF
  git -C "$DEMO" init -q -b main
  git -C "$DEMO" -c user.email=fixtures@taskman.local -c user.name="Taskman Fixtures" \
    add -A
  git -C "$DEMO" -c user.email=fixtures@taskman.local -c user.name="Taskman Fixtures" \
    commit -q -m "demo_client: initial fixture addon"

  # deliberately broken branch: unclosed XML tag -> real ParseError on Odoo boot
  git -C "$DEMO" checkout -q -b broken
  cat > "$DEMO/addons/taskman_demo/views/demo_widget_views.xml" <<'EOF'
<odoo>
  <record id="view_demo_widget_tree" model="ir.ui.view">
    <field name="name">taskman.demo.widget.tree</field>
    <field name="model">taskman.demo.widget</field>
    <field name="arch" type="xml">
      <tree>
        <field name="name">
      </tree>
    </field>
  </record>
</odoo>
EOF
  git -C "$DEMO" -c user.email=fixtures@taskman.local -c user.name="Taskman Fixtures" \
    commit -q -am "demo_client: deliberately broken XML for ParseError fixture"
  git -C "$DEMO" checkout -q main
else
  echo "[bootstrap] demo_client/ already present, skipping"
fi

echo "[bootstrap] done."
