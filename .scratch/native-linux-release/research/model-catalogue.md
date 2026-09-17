# The model catalogue: which models, and which support reasoning effort

Research for `.scratch/native-linux-release/issues/04-model-catalogue.md`.

All facts below were read from OpenAI's own documentation, its published OpenAPI
spec, or the vendored first-party Go SDK. **Every source was accessed on
2026-09-17.** Anything I could not establish from a primary source is called out
under [Gaps](#gaps-not-established-from-a-primary-source) rather than guessed.

Note that OpenAI's developer documentation now lives at `developers.openai.com`;
`platform.openai.com/docs/...` URLs redirect there. The API-reference paths under
`platform.openai.com` still resolve, but the canonical pages are the
`developers.openai.com` ones cited here.

---

## Headline: the fixed six-value effort list in the code is wrong in two ways

`internal/settings/settings.go` accepts exactly `none, minimal, low, medium,
high, xhigh`. Against the documented reality:

1. **`max` is missing.** It is a real, currently supported effort value and the
   top of the scale on every GPT-5.6 model and on GPT-6 Astra. The daemon
   currently cannot express the highest effort the default model supports.
2. **`minimal` is legacy.** It survives only on the original `gpt-5` family. It
   is absent from the documented effort list of every model from `gpt-5.1`
   onward, where `none` replaced it as the bottom of the scale.

So the list is simultaneously missing a value users will want and offering one
that the default model does not document. On top of that, the list being *fixed*
is itself the bug the ticket is about: the supported set genuinely differs per
model (GPT-5.5 has no `max`; GPT-6 Astra has no `none`), and so does the
*default* (`medium` on GPT-5.6, but `none` on GPT-5.4).

---

## 1. Which models to list, and their exact API identifiers

From the models index and the "all models" page
(<https://developers.openai.com/api/docs/models>,
<https://developers.openai.com/api/docs/models/all>, accessed 2026-09-17), and
cross-checked against the `ModelIdsShared` enum in OpenAI's published OpenAPI
spec (<https://github.com/openai/openai-openapi>, `openapi.yaml`, accessed
2026-09-17) and the `ChatModel` constants in `openai-go/v3 v3.61.0`
(`shared/shared.go`, the version already in this repo's `go.mod`).

The text/reasoning models worth offering in an `aos model set` dropdown:

| API id | Position (OpenAI's words) | Snapshots / aliases |
|---|---|---|
| `gpt-6-astra` | "Our most capable model, built for the hardest end-to-end work" | one snapshot, `gpt-6-astra`; no dated alias in the spec |
| `gpt-5.6-sol` | "Flagship model for complex professional work" | the alias `gpt-5.6` routes here |
| `gpt-5.6-terra` | balances intelligence and cost (the "mini" tier of the family) | one snapshot, `gpt-5.6-terra` |
| `gpt-5.6-luna` | "optimized for cost-sensitive workloads" | one snapshot, `gpt-5.6-luna` |
| `gpt-5.5` | previous generation, still current | `gpt-5.5-2026-04-23` |
| `gpt-5.4` | previous generation, still current | `gpt-5.4-mini`, `gpt-5.4-nano`, dated `-2026-03-17` snapshots |

`gpt-5`, `gpt-5.1`, `gpt-5.2`, `o3`, `gpt-4.1`, `gpt-4o` and friends are all
still in the spec's model-id enum, but the GPT-5 page itself says it is a
"previous intelligent reasoning model" and recommends GPT-6 Astra
(<https://developers.openai.com/api/docs/models/gpt-5>, accessed 2026-09-17).
I would not seed them into the dropdown; see the catalogue design note about
keeping the file open rather than closed.

The plan's default, **`gpt-5.6-terra`, is a real and current model id** — it
appears in the docs index, the OpenAPI `ModelIdsShared` enum and the SDK's
`ChatModelGPT5_6Terra` constant. The default is sound.

The `-chat-latest` and `-pro` variants deserve a deliberate decision rather than
inclusion by default: `gpt-5.6`-era chat models are documented as non-reasoning
(setting `reasoning_effort` on `gpt-5-chat-latest` is a documented 400), and the
`pro` variants are a different latency and price class. I did not chase their
effort lists, because they are out of scope for a default agent daemon.

## 2. Which models accept `reasoning.effort`, and which values each accepts

The superset of values, from the reasoning guide
(<https://developers.openai.com/api/docs/guides/reasoning>, accessed
2026-09-17):

> "Supported values are model-dependent and can include none, minimal, low,
> medium, high, xhigh, and max."

The same seven values, and the same model-dependence warning, are the canonical
enum in the OpenAPI spec (`ReasoningEffort`, `openapi.yaml`, accessed
2026-09-17, schema default `medium`) and in the vendored SDK:

```go
// openai-go/v3@v3.61.0 shared/shared.go:1096
// Constrains effort on reasoning for reasoning models. Currently supported values
// are `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. ...
// Not all reasoning models support every value.
```

Per model, from each model's own documentation page (all accessed 2026-09-17):

| Model | Documented effort values | Default | Source |
|---|---|---|---|
| `gpt-6-astra` | `low`, `medium`, `high`, `xhigh`, `max` — **no `none`, no `minimal`** | not documented (see Gaps) | [page](https://developers.openai.com/api/docs/models/gpt-6-astra) |
| `gpt-5.6-sol` | `none`, `low`, `medium`, `high`, `xhigh`, `max` | `medium` | [page](https://developers.openai.com/api/docs/models/gpt-5.6-sol) |
| `gpt-5.6-terra` | `none`, `low`, `medium`, `high`, `xhigh`, `max` | `medium` | [page](https://developers.openai.com/api/docs/models/gpt-5.6-terra) |
| `gpt-5.6-luna` | `none`, `low`, `medium`, `high`, `xhigh`, `max` | `medium` | [page](https://developers.openai.com/api/docs/models/gpt-5.6-luna) |
| `gpt-5.5` | `none`, `low`, `medium`, `high`, `xhigh` — **no `max`** | `medium` | [page](https://developers.openai.com/api/docs/models/gpt-5.5) |
| `gpt-5.4` | `none`, `low`, `medium`, `high`, `xhigh` — **no `max`** | **`none`** | [page](https://developers.openai.com/api/docs/models/gpt-5.4) |
| `gpt-5` (legacy) | `minimal`, `low`, `medium`, `high` — **no `none`, no `xhigh`, no `max`** | not documented | [page](https://developers.openai.com/api/docs/models/gpt-5) |

Two behaviours are worth encoding in the UI:

- **Unsupported effort is a hard error, not a silent clamp.** The reasoning
  guide states plainly: "GPT-6 Astra does not support none reasoning effort.
  Setting `reasoning.effort` (Responses) or `reasoning_effort` (Chat
  Completions) to none returns HTTP 400."
  (<https://developers.openai.com/api/docs/guides/reasoning>, accessed
  2026-09-17.) This is exactly the failure the ticket wants to prevent: today a
  user on `gpt-6-astra` can save `none` and every task will 400.
- **Migration advice for Astra.** The model-guidance page says that when moving
  off `none`/`minimal` on an earlier model, start at `low` on GPT-6 Astra
  (<https://developers.openai.com/api/docs/guides/latest-model>, accessed
  2026-09-17). That is a sensible value for the daemon to substitute when a
  saved effort is not valid for a newly selected model.

### `minimal` in particular

The lineage, from OpenAI's own launch post and model pages (accessed
2026-09-17): `minimal` was introduced with GPT-5
(<https://developers.openai.com/api/docs/models/gpt-5>). From GPT-5.1 onward
OpenAI replaced it with `none` — the GPT-5.1 developer post frames the
comparison as "GPT-5 with 'minimal' reasoning" versus "GPT-5.1 with no
reasoning" (<https://openai.com/index/gpt-5-1-for-developers/>).

So `minimal` is documented for the `gpt-5` family only, and for **none** of the
models this product should list. It remains in the wire enum (the SDK and the
OpenAPI spec both still carry it), so it is not invalid API-wide — but offering
it in the UI alongside `gpt-5.6-terra` is offering a value the selected model
does not document.

## 3. Does `GET /v1/models` report anything about effort support?

**No. Plainly: no.** This is the decisive answer for whether the catalogue can
ever be built at runtime, so I checked it twice, against both the reference page
and the machine-readable schema.

The `Model` object has exactly five fields
(<https://developers.openai.com/api/reference/resources/models/methods/list>,
and the `Model` schema in `openapi.yaml`, both accessed 2026-09-17):

| Field | Meaning |
|---|---|
| `id` | "The model identifier, which can be referenced in the API endpoints" |
| `object` | always `"model"` |
| `created` | Unix timestamp |
| `owned_by` | owning organization |
| `shutdown_date` | "The date when the model will shut down, or null if not announced" |

There is **no** field for reasoning-effort support, no capability flags, no
context window and no pricing. Worse for this purpose, the listing is scoped to
what the calling account can access and includes every embedding, audio, image
and moderation model, with nothing to distinguish a reasoning model from a
transcription one.

**Consequence for the design:** the effort catalogue cannot be discovered at
runtime. It has to ship as data. `GET /v1/models` is still useful for two
narrower jobs, and I'd suggest using it for exactly these and nothing more:

1. **Existence/entitlement check** — confirm a model id the user typed is one
   this API key can actually use, which is a better error than a 404 at first
   task.
2. **Retirement warning** — `shutdown_date` is genuinely useful and is the one
   piece of lifecycle data the API does volunteer. Surfacing "this model shuts
   down on 2026-xx-xx" in System Settings is cheap and would age well.

## 4. Pricing to seed `prices.yaml`

From <https://developers.openai.com/api/docs/pricing> (accessed 2026-09-17),
cross-checked against each individual model page. USD per 1M tokens, Standard
processing tier.

I checked this deliberately because an early search result claimed
`gpt-5.6-terra` was $2.50 / $15. **That is wrong** — both the pricing page and
the model page say $2.00 / $12.00. ($2.50 / $15.00 is `gpt-5.4`.) Worth
recording as a caution: the secondary summaries are conflating adjacent rows.

| Model | Input | Cached input | Output |
|---|---|---|---|
| `gpt-6-astra` | 10.00 | 1.00 | 50.00 |
| `gpt-5.6-sol` | 4.00 | 0.40 | 20.00 |
| `gpt-5.6-terra` | 2.00 | 0.20 | 12.00 |
| `gpt-5.6-luna` | 0.20 | 0.02 | 1.20 |
| `gpt-5.5` | 5.00 | 0.50 | 30.00 |
| `gpt-5.4` | 2.50 | 0.25 | 15.00 |
| `gpt-5` (legacy) | 1.25 | 0.125 | 10.00 |

The existing `usage.DefaultPrices` entry for `gpt-5.6-terra` (2.00 / 0.20 /
12.00, recorded 2026-09-14) is **still correct as of 2026-09-17**. No change
needed; it just needs company.

### The long-context surcharge, which the current `prices.yaml` cannot express

Every model page carries a variant of:

> "Prompts with >272K input tokens are priced at 2x input and 1.5x output for
> the full request."

and the pricing page lists the resulting long-context rates explicitly. I
verified the arithmetic is consistent across all six models (input and cached
input both double; output is 1.5x):

| Model | Long input | Long cached | Long output |
|---|---|---|---|
| `gpt-6-astra` | 20.00 | 2.00 | 75.00 |
| `gpt-5.6-sol` | 8.00 | 0.80 | 30.00 |
| `gpt-5.6-terra` | 4.00 | 0.40 | 18.00 |
| `gpt-5.6-luna` | 0.40 | 0.04 | 1.80 |
| `gpt-5.5` | 10.00 | 1.00 | 45.00 |
| `gpt-5.4` | 5.00 | 0.50 | 22.50 |

This matters for Cost Limits specifically. Note the surcharge applies to **the
full request**, not to the tokens past the threshold — so a 273K-token prompt
costs a little over twice a 271K-token one. With 1,050,000-token context windows
on these models, a long-context request is reachable in normal use, and a Cost
Limit computed from the short-context rate would under-count it by 2x. The
current `Price` struct (`Input, CachedInput, Output`) has nowhere to put this.
The YAML below adds an optional `long_context` block so the shape can carry it
when `internal/usage` is ready to use it; until then it is ignorable data.

Two further billing modifiers I found but did **not** chase to a full table,
since they are opt-in and out of scope for a default install: Flex / Priority /
Fast service tiers (`service_tier` in the API), and a stated 10% uplift for
regional-processing (data-residency) endpoints on newer models.

## 5. Is there a machine-readable source that could be refreshed without a release?

Partly — and the split is awkward. The short answer: **for effort values, yes,
but only the superset; for the per-model mapping and for prices, no.**

| Source | Machine-readable? | Gives model ids? | Per-model efforts? | Prices? |
|---|---|---|---|---|
| `GET /v1/models` | yes (JSON API) | yes (entitled ids) | **no** | **no** |
| [openai-openapi](https://github.com/openai/openai-openapi) `openapi.yaml` / `.json` | yes, 3.4 MB spec | yes (`ModelIdsShared` enum) | **no** (one flat `ReasoningEffort` enum) | **no** (I grepped: the only "pricing" hits are prose links and `service_tier` descriptions) |
| Docs `.md` mirrors, e.g. `/api/docs/models.md` | text, but prose | only inside link text | **no** | **no** |
| Pricing page | **HTML only** — no CSV/JSON/downloadable form offered | yes | n/a | yes |

The OpenAPI spec is the strongest first-party machine-readable artefact and is
worth knowing about: it pins the seven-value effort enum and the full model-id
enum, and it is versioned in git so a refresh is a diff. But it flattens effort
into a single enum with the note "Not all reasoning models support every value",
which is precisely the information the catalogue needs and the spec declines to
give.

**So the per-model effort mapping exists only as prose on individual model
pages, and pricing exists only as an HTML table.** Neither can be refreshed
automatically from a primary source without scraping, and I would not build
scraping of a docs page into a release product.

That points at the design the YAML below assumes: ship a **seed** catalogue as
data, in a user-editable file, exactly as `prices.yaml` already works. Then a
price change or a new model is a text edit by the operator, not a release. That
is the same trade `internal/usage` already made, and it is the right one — it
just needs to cover efforts too.

---

## Proposed `models.yaml`

Seeds `/var/lib/aos/models.yaml` on first run, the way `usage.DefaultPrices`
seeds `prices.yaml`. Keyed by model id, so the existing
`Prices.Lookup` dash-prefix rule (a dated snapshot falls back to the longest
matching base name) works unchanged for `gpt-5.5-2026-04-23` and friends.

The `price` block is deliberately the same three keys the current `Price` struct
already parses, nested one level down — so `models.yaml` can either supersede
`prices.yaml` or be read alongside it during a transition.

```yaml
# The model catalogue for aosd: which models System Settings offers, which
# reasoning efforts each one accepts, and what each costs.
#
# Read from OpenAI's documentation on 2026-09-17:
#   efforts + defaults  https://developers.openai.com/api/docs/models/<id>
#   prices              https://developers.openai.com/api/docs/pricing
#
# OpenAI does not publish this mapping in machine-readable form, and
# GET /v1/models does not report effort support, so this file is the source of
# truth. Edit it when OpenAI changes prices or ships a model:
#   docker compose exec aos nano /var/lib/aos/models.yaml
# aosd re-reads it before every model request.
#
# efforts: the values this model accepts. Sending any other value is an API
#   error (HTTP 400), not a silent clamp, so System Settings offers only these.
# default_effort: what OpenAI uses when the request omits reasoning.effort.
# price: USD per 1M tokens, Standard tier, prompts up to 272K input tokens.
# long_context: USD per 1M tokens once a prompt exceeds 272K input tokens.
#   OpenAI applies these to the WHOLE request, not just the excess.

version: 1

models:
  gpt-6-astra:
    label: GPT-6 Astra
    efforts: [low, medium, high, xhigh, max]   # note: no "none", no "minimal"
    default_effort: null                       # not documented by OpenAI; see notes
    context_window: 1050000
    price:        { input: 10.00, cached_input: 1.00, output: 50.00 }
    long_context: { input: 20.00, cached_input: 2.00, output: 75.00 }

  gpt-5.6-sol:
    label: GPT-5.6 Sol
    aliases: [gpt-5.6]
    efforts: [none, low, medium, high, xhigh, max]
    default_effort: medium
    context_window: 1050000
    price:        { input: 4.00, cached_input: 0.40, output: 20.00 }
    long_context: { input: 8.00, cached_input: 0.80, output: 30.00 }

  gpt-5.6-terra:
    label: GPT-5.6 Terra
    default: true                              # aosd's built-in default model
    efforts: [none, low, medium, high, xhigh, max]
    default_effort: medium
    context_window: 1050000
    price:        { input: 2.00, cached_input: 0.20, output: 12.00 }
    long_context: { input: 4.00, cached_input: 0.40, output: 18.00 }

  gpt-5.6-luna:
    label: GPT-5.6 Luna
    efforts: [none, low, medium, high, xhigh, max]
    default_effort: medium
    context_window: 1050000
    price:        { input: 0.20, cached_input: 0.02, output: 1.20 }
    long_context: { input: 0.40, cached_input: 0.04, output: 1.80 }

  gpt-5.5:
    label: GPT-5.5
    efforts: [none, low, medium, high, xhigh]  # note: no "max"
    default_effort: medium
    context_window: 1050000
    price:        { input: 5.00, cached_input: 0.50, output: 30.00 }
    long_context: { input: 10.00, cached_input: 1.00, output: 45.00 }

  gpt-5.4:
    label: GPT-5.4
    efforts: [none, low, medium, high, xhigh]  # note: no "max"
    default_effort: none                       # not medium, unlike the 5.6 family
    context_window: 1050000
    price:        { input: 2.50, cached_input: 0.25, output: 15.00 }
    long_context: { input: 5.00, cached_input: 0.50, output: 22.50 }
```

### Notes on consuming it

- **Keep the file open, not closed.** A model id absent from `models.yaml`
  should stay *usable* — falling back to today's "non-empty, under 100 chars, no
  whitespace" check plus the full seven-value effort enum, with a visible
  "unknown model, cost not tracked" note. This matters because OpenAI ships
  models faster than this product will cut releases, and a strict allow-list
  would make every new model unreachable until a release. The dropdown becomes
  "the catalogue, plus a free-text escape hatch", which also satisfies
  "reject an invalid model" for every model the user is realistically on. It
  matches how `prices.yaml` already treats an unknown model (unknown cost, no
  Cost Limit).
- **`default_effort: null` for `gpt-6-astra` is deliberate** — it records
  "OpenAI does not document this", not "there is no default". The UI should show
  the model's own default rather than inventing one; see Gaps.
- **Re-validate effort on model change.** Because the sets genuinely differ, the
  saved effort can become invalid when the model changes (`none` is valid on
  `gpt-5.6-terra` and a 400 on `gpt-6-astra`). On a model change, if the saved
  effort is not in the new model's set, substitute — OpenAI's own migration
  advice is to start at `low` when coming from `none`/`minimal`.
- **`internal/settings` should validate the pair, not the parts.** Today the
  model and the effort validate independently, which is what allows the invalid
  combination to be saved at all.

---

## Gaps: not established from a primary source

Stated explicitly, per the ticket's instruction to flag rather than invent.

1. **The default reasoning effort for `gpt-6-astra`.** Its model page lists the
   five accepted values but does not state a default, and the reasoning guide
   gives defaults only for GPT-5.5 and GPT-5.6. The OpenAPI `ReasoningEffort`
   schema carries `default: medium`, but that is the *schema-wide* default and
   the same document says support is model-dependent — I do not treat it as
   evidence of Astra's actual server-side default. **Unknown.**
2. **The default reasoning effort for `gpt-5` (legacy).** Not stated on its
   page. Unknown, and low-stakes since I am not proposing to list it.
3. **Whether `minimal` is silently accepted on post-5.1 models.** The value is
   still in the wire enum, but no page documents whether sending it to
   `gpt-5.6-terra` 400s, is treated as `none`, or is treated as `low`. Only the
   Astra/`none` case has a documented error. I did not test against the live API
   — that needs a key and would be an empirical result, not a documented one.
   **If this matters, the cheap experiment is one API call per model.**
4. **`-pro` and `-chat-latest` effort sets.** Not chased; out of scope. The
   related documented fact is that `gpt-5-chat-latest` rejects `reasoning_effort`
   as a non-reasoning model.
5. **Whether the 272K long-context threshold is identical across all six
   models.** Each page states ">272K" individually and the pricing arithmetic is
   consistent, so this is well-evidenced, but I did not find a single page
   asserting it as a family-wide rule.
6. **Exact effect of the Flex / Priority / Fast service tiers and the stated 10%
   regional-processing uplift on these per-token prices.** Documented as
   existing; I did not gather the rate tables.
7. **Retirement dates for `gpt-5.4` / `gpt-5.5`.** Neither page showed a
   deprecation notice or shutdown date as of 2026-09-17. `GET /v1/models`
   `shutdown_date` is the live source for this.

---

## Sources

All accessed **2026-09-17**.

- Reasoning guide — <https://developers.openai.com/api/docs/guides/reasoning>
- Model guidance — <https://developers.openai.com/api/docs/guides/latest-model>
- Models index — <https://developers.openai.com/api/docs/models>
- All models — <https://developers.openai.com/api/docs/models/all>
- GPT-6 Astra — <https://developers.openai.com/api/docs/models/gpt-6-astra>
- GPT-5.6 Sol — <https://developers.openai.com/api/docs/models/gpt-5.6-sol>
- GPT-5.6 Terra — <https://developers.openai.com/api/docs/models/gpt-5.6-terra>
- GPT-5.6 Luna — <https://developers.openai.com/api/docs/models/gpt-5.6-luna>
- GPT-5.5 — <https://developers.openai.com/api/docs/models/gpt-5.5>
- GPT-5.4 — <https://developers.openai.com/api/docs/models/gpt-5.4>
- GPT-5 (legacy) — <https://developers.openai.com/api/docs/models/gpt-5>
- Pricing — <https://developers.openai.com/api/docs/pricing>
- List models (API reference) — <https://developers.openai.com/api/reference/resources/models/methods/list>
- GPT-5.1 for developers (the `minimal` → `none` change) — <https://openai.com/index/gpt-5-1-for-developers/>
- OpenAPI spec — <https://github.com/openai/openai-openapi> (`openapi.yaml`, `ReasoningEffort`, `ModelIdsShared`, `Model` schemas)
- Vendored SDK — `openai-go/v3 v3.61.0`, `shared/shared.go` (`ReasoningEffort`, `ChatModel` constants)
