#!/bin/bash
# Enforce MVC + core/ext/pitago boundaries (P0).
# Fails on forbidden imports; run in CI and before commit.
set -e
cd "$(dirname "$0")/.."
fail=0
bad() { echo "LAYER VIOLATION: $1"; fail=1; }

# Pure domain never imports UI/controller.
for p in src/ext src/pitago src/components src/extension src/pirpc src/update; do
  if grep -rn "pitago/src/app\|pitago/src/builtin" "$p" --include="*.go" | grep -v "_test.go" | grep -q .; then
    bad "$p imports app/builtin (must be one-way: app -> $p)"
    grep -rn "pitago/src/app\|pitago/src/builtin" "$p" --include="*.go" | grep -v "_test.go"
  fi
done

# Backend transport never imports UI.
if grep -rn "pitago/src/app\|pitago/src/builtin\|pitago/src/components" src/pirpc --include="*.go" | grep -v "_test.go" | grep -q .; then
  bad "src/pirpc imports UI layer"
fi

# ext (pi-extension) and pitago-only never cross-import.
if grep -rn "pitago/src/ext" src/pitago --include="*.go" | grep -v "_test.go" | grep -q .; then
  bad "src/pitago imports src/ext (keep pi-extension vs pitago-only separate)"
fi
if grep -rn "pitago/src/pitago" src/ext --include="*.go" | grep -v "_test.go" | grep -q .; then
  bad "src/ext imports src/pitago (keep pi-extension vs pitago-only separate)"
fi

if [ "$fail" = 1 ]; then exit 1; fi
# Code comments must be English-only: fail on Vietnamese diacritics in
# // comment lines (UI strings and test fixtures may stay Vietnamese).
if grep -rn "^[[:space:]]*//.*[àáạảãâầấậẩẫăằắặẳẵèéẹẻẽêềếệểễìíịỉĩòóọỏõôồốộổỗơờớợởỡùúụủũưừứựửữỳýỵỷỹđĐ]" src/ --include="*.go" | grep -q .; then
  echo "COMMENT VIOLATION: code comments must be English (found Vietnamese diacritics in // lines):"
  grep -rn "^[[:space:]]*//.*[àáạảãâầấậẩẫăằắặẳẵèéẹẻẽêềếệểễìíịỉĩòóọỏõôồốộổỗơờớợởỡùúụủũưừứựửữỳýỵỷỹđĐ]" src/ --include="*.go"
  exit 1
fi
echo "layers OK: MVC + core/ext/pitago boundaries hold"
