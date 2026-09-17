# The model catalogue: which models, and which support reasoning effort

Type: research
Status: resolved
Blocked by: —

## Question

`aos model set <name>` must reject an invalid model, the UI needs a dropdown rather than a free-text field, and effort must only be offered "if the currently selected model supports it". That needs a catalogue mapping model → supported reasoning efforts (and ideally price, since `internal/usage` already reads a user-editable `prices.yaml` for Cost Limits).

Today `internal/settings/settings.go` validates a model as "non-empty, under 100 characters, no whitespace" and offers a fixed six-value effort list (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`) with no per-model awareness. The plan's default model is `gpt-5.6-terra`.

Find out, from OpenAI's own documentation:

1. Which current OpenAI models are worth listing for this product, and their exact API identifiers.
2. Which of them accept a `reasoning.effort` parameter, and which effort values each accepts — the six-value list in the code needs checking against reality.
3. Whether `GET /v1/models` reports anything about effort support (if not, say so plainly — it decides whether the catalogue can ever be built at runtime).
4. Current input/output pricing for those models, to seed `prices.yaml`.
5. Whether OpenAI publishes a machine-readable source for any of this that could be refreshed without a release.

Write findings to `.scratch/native-linux-release/research/model-catalogue.md` with source URLs and access dates. Do not edit any file outside that directory.

## Answer

Resolved 2026-09-17 by research. Full findings, with source URLs, access dates and an explicit gaps section: [research/model-catalogue.md](../research/model-catalogue.md).

**The catalogue cannot be built at runtime.** `GET /v1/models` returns only `id`, `object`, `created`, `owned_by` and `shutdown_date` — nothing about reasoning effort. OpenAI publishes the per-model effort mapping as prose only; the OpenAPI spec flattens effort into a single enum annotated "not all models support every value", which is precisely the missing information. So the catalogue must ship as data. The endpoint is still worth calling for an entitlement check and to surface `shutdown_date`.

**The fixed six-value effort list in `internal/settings/settings.go:74` is wrong twice over**, and the deeper defect is that it is fixed at all:

- `max` is missing, though it is the top of the scale on the default model.
- `minimal` is legacy, documented only for the original `gpt-5` family and replaced by `none` from `gpt-5.1` onward.
- The supported set and the default genuinely differ per model, and **an unsupported effort is an HTTP 400, not a silent clamp**. A user today can save an effort their model rejects and every Task then fails.

This is a live defect in the shipped code, not only a gap in the new work, and it is the strongest argument for the per-model catalogue.

**`gpt-5.6-terra` is confirmed current** and its price in `usage.DefaultPrices` (2.00 / 0.20 / 12.00) is still correct — verified against the repo.

**A long-context surcharge exists that the code cannot represent.** Prompts beyond 272K input tokens are billed at a multiple on the *whole* request. `usage.Price` has only `Input, CachedInput, Output` (verified at `internal/usage/usage.go:35`), so Cost Limits would under-count on long contexts — reachable given the context windows involved. The proposed `models.yaml` carries an optional `long_context` block for this.

**Recommendation carried forward**: the catalogue is a seed file at `/var/lib/aos/models.yaml`, reusing the three price keys `usage.Price` already parses, and it stays **open rather than a strict allow-list** — an unlisted model remains usable with a "cost not tracked" note, so a new OpenAI model does not require a release of AOS before it can be selected.

**Two gaps need a live API call to settle** (one request per model, so this spends Aman's key and is his to authorise): GPT-6 Astra's server-side default effort, which is documented nowhere; and whether `minimal` is silently accepted or rejected on post-5.1 models. Five further gaps are listed in the research file.

**Feeds**: the effort/model validation in */etc/aos/config.yml: schema, validation and the `aos config` verbs*, and the M6 sub-tasks for `aos model get|set`, `aos effort get|set` and the System Settings dropdown.
