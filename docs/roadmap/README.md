# Roadmap material — NOT YET IMPLEMENTED

> ⚠️ **Everything under `docs/roadmap/` describes planned features that the engine
> does not implement yet.** These schemas, examples, and specs are design targets for
> the milestones in [`/ROADMAP.md`](../../ROADMAP.md), not runnable configuration.
>
> The YAML loader uses **strict decoding**, so any config file here that uses a planned
> key (e.g. `cpn_enabled`, `token_schemas`, `token_selection`) will be **rejected with an
> error** if you try to load it today. That is intentional — it prevents silently ignoring
> features that don't exist.

## Contents

### `cpn/` — Colored Petri Nets / Smart Tokens (planned, milestone **M2**)
- `CPN_YAML_SCHEMA.md` — proposed YAML schema for token-carrying workflows.
- `CPN_YAML_QUICK_REFERENCE.md` — quick reference for the proposed CPN YAML keys.
- `cpn_schema.json` — JSON Schema for the proposed CPN YAML.
- `cpn_example_minimal.yaml` — minimal example of the proposed syntax.

### `examples/banking_system/` — CPN example (planned, milestone **M2**)
A worked banking example built around `cpn_enabled: true` and token schemas. It documents
the intended modelling approach; it will become a runnable example once M2 lands.

## Why these are here and not in `yaml/` or `examples/`

Shipping these alongside working config would imply they run. They don't. Keeping them in
`docs/roadmap/` keeps the promise/reality boundary honest: everything outside this folder
is expected to actually work.
