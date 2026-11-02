"""Validate and package the private Surmai Travel plugin. No credentials bundled."""
import json
from pathlib import Path
import re
import urllib.request
import zipfile

import jsonschema
import yaml

ROOT = Path(__file__).resolve().parent
SOURCE = ROOT / "surmai-travel"
OUTPUT = ROOT.parent / "dist" / "surmai-travel-0.1.1.zip"

def build():
    for name in ("plugin",):
        data = json.loads((SOURCE / f"{name}.json").read_text(encoding="utf-8"))
        with urllib.request.urlopen(data["$schema"], timeout=20) as response:
            schema = json.load(response)
        jsonschema.validate(data, schema)
    manifest = json.loads((SOURCE / "plugin.json").read_text())
    interface = manifest["extensions"]["com.openai"]["interface"]
    assert manifest["name"] == SOURCE.name
    assert len(interface["shortDescription"]) <= 30
    for field in ("logo", "composerIcon"):
        target = (SOURCE / interface[field]).resolve()
        assert target.is_relative_to(SOURCE.resolve()) and target.is_file()
    skills = list((SOURCE / "skills").glob("*/SKILL.md"))
    assert len(skills) == 3
    for skill in skills:
        content = skill.read_text(encoding="utf-8")
        frontmatter = yaml.safe_load(content.split("---", 2)[1])
        assert frontmatter["name"] == skill.parent.name
        assert frontmatter["description"]
    files = sorted(p for p in SOURCE.rglob("*") if p.is_file())
    assert all(not p.is_symlink() for p in files)
    assert not any(re.search(r"(?:sk-proj-|ghp_|gho_|Bearer eyJ)", p.read_text(errors="ignore")) for p in files if p.suffix != ".png")
    OUTPUT.parent.mkdir(exist_ok=True)
    with zipfile.ZipFile(OUTPUT, "w", zipfile.ZIP_DEFLATED) as archive:
        for path in files:
            archive.write(path, path.relative_to(SOURCE.parent).as_posix())
    with zipfile.ZipFile(OUTPUT) as archive:
        assert archive.testzip() is None
        assert set(archive.namelist()) == {p.relative_to(SOURCE.parent).as_posix() for p in files}
        for name in ("plugin",):
            assert json.loads(archive.read(f"surmai-travel/{name}.json"))
    print(f"Validated {len(skills)} skills and {len(files)} files: {OUTPUT}")

if __name__ == "__main__":
    build()
