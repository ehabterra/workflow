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

> **Note:** Colored Petri Nets (Smart Tokens) have since **shipped** — the planning
> documents that used to live in `cpn/` are deleted. The implemented mechanism is the
> polymorphic `initial_marking` key; see [`docs/guides/CPN_GUIDE.md`](../guides/CPN_GUIDE.md)
> and the runnable [`examples/cpn_batch_processing`](../../examples/cpn_batch_processing)
> example.

### `examples/banking_system/` — historical CPN planning example
A worked banking example built around the **proposed, never-implemented**
`cpn_enabled`/`token_schemas` schema. It is kept as a historical planning document; its
YAML will not load. For the shipped CPN mechanism see
[`docs/guides/CPN_GUIDE.md`](../guides/CPN_GUIDE.md).

## Why these are here and not in `yaml/` or `examples/`

Shipping these alongside working config would imply they run. They don't. Keeping them in
`docs/roadmap/` keeps the promise/reality boundary honest: everything outside this folder
is expected to actually work.
