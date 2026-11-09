---
status: proposed
date: 2026-11-09
decision-makers: joestump-agent (proposed; issue #340 inherited two contradictory prompt rules)
---

# ADR-0037: Prompt Templates Are Authoritative, With One Minimal Built-In Fallback Per Feature

## Context and Problem Statement

Two governing specs on `main` disagree about whether Spotter may contain a hardcoded prompt. The vibes spec says prompts come from files and "Hardcoded prompt strings are NOT permitted" (`vibes-ai-mixtape-engine` REQ-VIBES-013). The similar-artists spec says the opposite for its own feature: if the template is nil or fails to execute, the service "MUST use `fallbackPrompt()`", a prompt built in Go (`similar-artists-discovery` REQ-SIM-002 and REQ-SIM-011).

The code follows the second rule in both features. `MixtapeGenerator.renderPrompt` falls back to `fallbackPrompt()` when the `generate_mixtape` template is absent, and `SimilarArtistsService` does the same for `enrich_artist`. So one of the two sentences is simply wrong, and story #340 - which consolidates the generation path and the matching stack that both features share - cannot describe the resulting behavior without picking a side. Silently choosing one in code is the worse option: the specs are what the next contributor reads, and epic #304 exists precisely to retire that kind of divergence.

## Decision Drivers

* Templates in `vibes.prompts_directory` are the thing operators actually tune; a prompt buried in Go cannot be changed without a rebuild
* A misconfigured or missing prompts directory is a deployment mistake, and a mistake of that size should cost prompt quality, not the whole AI feature
* Both features already behave this way, so the option that matches the running system also matches what users have
* The fallback must stay a last resort and stay visible: it has to be obvious in the logs and in the persisted `generation_prompt` which prompt produced a result
* A fallback is only acceptable while it keeps the response contract each spec defines (REQ-VIBES-015, REQ-SIM-011), because the parser behind it is shared
* Fallback prompts contain no secrets and no user data beyond what the template would have carried anyway

## Considered Options

* **Option A: templates authoritative, one documented fallback per feature** — amend REQ-VIBES-013 to permit a single last-resort prompt per LLM feature; nothing in the code changes shape
* **Option B: no fallback anywhere** — amend REQ-SIM-011 to match REQ-VIBES-013 and make both `renderPrompt` paths fail closed. This is stricter, but it turns a missing directory into a hard outage of mixtape generation and similar-artist discovery, and it rewrites behavior users have today for a documentation reason
* **Option C: leave both specs and add a prose note** — no contradiction is resolved, it is only annotated, which is the state epic #304 exists to end

## Decision Outcome

Chosen option A. The templates remain authoritative, and each LLM feature keeps exactly one minimal fallback prompt in Go, used only when its template is unavailable or fails to execute. In this PR `docs/openspec/specs/vibes-ai-mixtape-engine/spec.md` REQ-VIBES-013 is amended to say that, and both specs list this ADR as governing so the shared policy is findable from either feature. The vibes generator also gains the warn-level log the policy assumes, naming the template and the configured directory, since the fallback branch was previously silent.

If a maintainer prefers option B, the change is localized: revert the REQ-VIBES-013 and REQ-SIM-011 wording, and have both `renderPrompt` implementations return an error instead of a prompt string.

## Consequences

* One prompt policy for both AI features, and no sentence in either spec that the other contradicts
* An instance with an unreadable or empty `vibes.prompts_directory` still generates mixtapes and still finds similar artists, at reduced quality, with a warning in the log; it no longer silently depends on undeclared Go strings
* "No hardcoded prompt strings" now means what it is aiming at: prompt *content* lives in templates, and prompt logic does not spread through handlers and services; the two declared fallbacks are the whole exception
* Each fallback is a single function per feature and must keep emitting the JSON shape its spec names, so `parseAIResponse` keeps working whichever prompt produced the call
* The persisted `generation_prompt` records the fallback text when it was used, so a mixtape generated off-template is distinguishable after the fact
* The fallback prompt text is still a Go string, so tuning it takes a rebuild - which is exactly why templates stay the source of truth

## References

* [Issue #340](https://github.com/joestump/spotter/issues/340) — deduplicate mixtape generation; unify vibes fuzzy matching
* [Issue #320](https://github.com/joestump/spotter/issues/320) — epic: vibes AI stability and consolidation
* [Issue #304](https://github.com/joestump/spotter/issues/304) — epic: spec governance cleanup
* `docs/openspec/specs/vibes-ai-mixtape-engine/spec.md` — REQ-VIBES-013, REQ-VIBES-015
* `docs/openspec/specs/similar-artists-discovery/spec.md` — REQ-SIM-002, REQ-SIM-011
* [ADR-0008](./ADR-0008-openai-api-litellm-compatible-llm-backend.md) — the OpenAI-compatible backend these prompts are sent to
* [ADR-0017](./ADR-0017-vibes-generator-interface-abstraction.md) — the generator abstraction this policy applies to
