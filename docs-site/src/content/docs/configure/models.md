---
title: Models and reasoning effort
description: Which models are available, what reasoning effort does, what each costs, and how to change or add models.
---

Every Task runs on one OpenAI model, chosen in **System Settings → Agent** or with `aos config set model …`.
The default is **GPT-5.6 Terra** (`gpt-5.6-terra`).

## The shipped catalogue

| Model id | Name | Reasoning efforts accepted | Default effort | Price (USD per 1M tokens: input / cached / output) |
| --- | --- | --- | --- | --- |
| `gpt-6-astra` | GPT-6 Astra | low · medium · high · xhigh · max | *not documented* | $10.00 / $1.00 / $50.00 |
| `gpt-5.6-sol` | GPT-5.6 Sol | none · low · medium · high · xhigh · max | medium | $4.00 / $0.40 / $20.00 |
| `gpt-5.6-terra` ⭐ | GPT-5.6 Terra | none · low · medium · high · xhigh · max | medium | $2.00 / $0.20 / $12.00 |
| `gpt-5.6-luna` | GPT-5.6 Luna | none · low · medium · high · xhigh · max | medium | $0.20 / $0.02 / $1.20 |
| `gpt-5.5` | GPT-5.5 | none · low · medium · high · xhigh | medium | $5.00 / $0.50 / $30.00 |
| `gpt-5.4` | GPT-5.4 | none · low · medium · high · xhigh | none | $2.50 / $0.25 / $15.00 |

⭐ = the default. `gpt-5.6` is an alias for `gpt-5.6-sol`. Every model listed has a context window of about
1.05 million tokens. Once a prompt goes over 272K input tokens, OpenAI charges long-context prices **for the
whole request**; those prices are in the catalogue file too.

:::note
These prices were copied from OpenAI's documentation when the catalogue was written. OpenAI's own pricing page
is always the authority. Update the catalogue when it changes (see below).
:::

## Choosing a model

| If you want… | Try |
| --- | --- |
| A good balance of quality and cost for everyday admin work | `gpt-5.6-terra` (the default) |
| The cheapest option for simple, repetitive Tasks | `gpt-5.6-luna` |
| Stronger reasoning for tricky debugging or multi-step setups | `gpt-5.6-sol` |
| The most capable model, cost no object | `gpt-6-astra` |

## Reasoning effort

**Reasoning effort** is how much the model thinks before it acts. Higher effort is slower and uses more
tokens, and often gets better results on hard problems. Leave it **empty** to use the model's own default.

```bash
sudo aos config set model gpt-5.6-sol
sudo aos config set reasoning_effort high
sudo aos config set reasoning_effort ""      # back to the model's default
```

Each model accepts only certain values, and sending any other one is an API error. So System Settings offers
only the efforts the chosen model supports, and `aos config set` refuses the rest.

## Editing the catalogue

The catalogue isn't hard-coded. It's a file you can edit: **`/var/lib/aos/models.yaml`**, written on first
start. Edit it when OpenAI changes a price or ships a new model:

```bash
sudo nano /var/lib/aos/models.yaml
```

Each entry looks like this:

```yaml
models:
  gpt-5.6-terra:
    label: GPT-5.6 Terra
    default: true                 # at most one model may be the default
    efforts: [none, low, medium, high, xhigh, max]
    default_effort: medium
    context_window: 1050000
    price:        { input: 2.00, cached_input: 0.20, output: 12.00 }
    long_context: { input: 4.00, cached_input: 0.40, output: 18.00 }
```

The daemon re-reads the file before it shows the model menus in System Settings. A typo, such as an effort
that doesn't exist, is reported when the file is loaded, rather than as a failed API call on your next Task.

**Any model id works, even one that isn't listed.** The catalogue isn't an allow-list. An unlisted model is
offered every effort value, and its cost isn't tracked (so cost limits can't see it).

On Docker Compose, edit the same file inside the container:

```bash
docker compose exec aos nano /var/lib/aos/models.yaml
```
