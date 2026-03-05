# Benchmark Summary

Generated: 2026-03-05 08:07 UTC  |  Results: `20260305-085732-ollama-jina-embeddings-v2-base-code`

| Scenario | Description |
|----------|-------------|
| **baseline** | All default Claude tools, no MCP |
| **solo** | `semantic_search` MCP tool only |
| **together** | All default tools + MCP |

## Overall: Aggregated by Scenario

Totals across all 8 questions × 1 models.

| Model | Scenario | Total Time | Total Input Tok | Total Output Tok | Total Cost (USD) |
|-------|----------|------------|-----------------|------------------|------------------|
| **haiku** | baseline | 503.3s | 328 | 31482 | $1.1100 |
| **haiku** | solo | 346.3s | 664 | 37007 | $0.7727 |
| **haiku** | together | 356.4s | 1729 | 37308 | $0.8238 |

---

## go-registry-concurrency [go / hard]

> How does TSDB compaction work end-to-end? Explain the Compactor interface, LeveledCompactor, and how the DB triggers compaction. Show relevant types, interfaces, and key method signatures.

### Time & Tokens

| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |
|-------|----------|----------|-----------|------------|------------|------------|--------|
| **haiku** | baseline | 74.7s | 34 | 126886 | 4919 | $0.1593 |  |
| **haiku** | solo | 41.4s | 50 | 111245 | 4776 | $0.0755 | 🏆 Winner |
| **haiku** | together | 40.6s | 42 | 195821 | 3403 | $0.0949 |  |

### Quality Ranking (Opus 4.6)



## Content Quality

**haiku/solo** — Most thorough and well-structured answer with accurate code signatures, clear explanation of the `DB.run()` trigger loop, detailed `selectDirs` logic with a concrete example of range leveling, and an excellent ASCII flow diagram summarizing the full lifecycle; line references are consistent and specific.

**haiku/together** — Strong coverage with good structure and accurate interfaces/types; includes useful details like stale series compaction triggers and leveling strategy math, but some code blocks are more paraphrased than exact (e.g., `Plan()` internals) and the explanation feels slightly more scattered.

**haiku/baseline** — Comprehensive and covers all the right areas with good code snippets, but includes some inaccuracies in code reproduction (e.g., the `BlockMeta` struct uses `string` for ULID instead of `ulid.ULID`, and `DB.Compact()` code is more reconstructed than quoted); the summary lifecycle list at the end is helpful but the answer is the most verbose of the three.

## Efficiency

haiku/solo delivers the best answer at the lowest cost ($0.076) and fastest time (41.4s), using significantly fewer input tokens than together and costing half of baseline. haiku/together is similarly fast but costs 25% more for a slightly weaker answer, while baseline takes nearly twice as long at double the cost.

## Verdict

**Winner: haiku/solo**

---

## py-django-queryset [python / hard]

> How does the Django QuerySet evaluation and filtering pipeline work? Explain QuerySet chaining, lazy evaluation, the Query class, how lookups and filters are compiled into SQL, and how the Manager ties it all together. Show key classes and method signatures.

### Time & Tokens

| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |
|-------|----------|----------|-----------|------------|------------|------------|--------|
| **haiku** | baseline | 37.5s | 42 | 169110 | 4613 | $0.0973 |  |
| **haiku** | solo | 35.9s | 34 | 56951 | 4536 | $0.0690 | 🏆 Winner |
| **haiku** | together | 49.4s | 52 | 233784 | 6245 | $0.0993 |  |

### Quality Ranking (Opus 4.6)

## Content Quality

**haiku/solo** is the best answer — it provides the most structured and detailed explanation with accurate file:line references (e.g., `django-query.py:2137-2146`, `django-query.py:2168-2172`), covers all pipeline stages including the ModelIterable execution path and iterator protocol, and includes clear section headers with a complete end-to-end flow diagram.

**haiku/together** is a close second — equally comprehensive with good file references and adds a useful "Key Characteristics" summary table, but is more verbose (6245 output tokens vs 4536) without proportionally more insight, and some sections feel repetitive (the Q object section re-explains flattening that's already clear from the code).

**haiku/baseline** is solid but slightly less polished — covers all the same components correctly with proper code excerpts and line references, but the execution flow summary is simpler and it lacks the ModelIterable/compiler execution detail that the other two include.

## Efficiency

Solo is the clear efficiency winner: lowest cost ($0.069 vs $0.097/$0.099), fastest runtime (35.9s), and fewest input tokens, while producing quality on par with or better than the other runs. Together costs 44% more than solo for marginal quality difference, and baseline costs 41% more with slightly less complete output.

## Verdict

**Winner: haiku/solo**

---

## ts-disposable-events [typescript / hard]

> How do Disposable and IDisposable work together with the EventEmitter system? Explain the lifecycle management pattern, how listeners are registered and cleaned up, and how events are typed and fired. Show key interfaces and class relationships.

### Time & Tokens

| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |
|-------|----------|----------|-----------|------------|------------|------------|--------|
| **haiku** | baseline | 92.9s | 18 | 51228 | 3165 | $0.2176 |  |
| **haiku** | solo | 34.5s | 82 | 193322 | 3639 | $0.0740 |  |
| **haiku** | together | 39.1s | 42 | 176784 | 4577 | $0.0785 | 🏆 Winner |

### Quality Ranking (Opus 4.6)

## Content Quality

**haiku/together** is the most comprehensive, with accurate code excerpts, a clear registration flow diagram, three-level cleanup pattern explanation, EmitterOptions lifecycle hooks, the Relay example, leak detection details, and a summary table — all with specific file:line references.

**haiku/baseline** covers nearly the same ground with correct code and good structure, includes the class relationship diagram and specialized emitter variants (AsyncEmitter, PauseableEmitter, DebounceEmitter), but some code snippets appear slightly paraphrased rather than exact (e.g., DisposableStore using array instead of Set), and line references are less precise.

**haiku/solo** is accurate and well-structured with good line references and the Relay example, but is noticeably less detailed — omits leak detection, delivery queue internals, and the three-level cleanup taxonomy that the other two include.

## Efficiency

Solo is the fastest (34.5s) and cheapest ($0.074) while baseline is dramatically more expensive ($0.218) at 2.7x the runtime. Together is only marginally more expensive than solo ($0.078) but produces the most thorough answer. Together offers the best quality-to-cost ratio — nearly identical cost to solo but substantially richer content.

## Verdict

**Winner: haiku/together**

---

## java-petclinic-domain [java / hard]

> How is the PetClinic domain model structured? Explain the entity hierarchy (Owner, Pet, Visit, Vet), how JPA/Hibernate maps the relationships, and how the repository layer exposes data access. Show key classes, annotations, and method signatures.

### Time & Tokens

| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |
|-------|----------|----------|-----------|------------|------------|------------|--------|
| **haiku** | baseline | 35.1s | 42 | 153032 | 3618 | $0.0545 |  |
| **haiku** | solo | 23.8s | 34 | 38883 | 3366 | $0.0461 | 🏆 Winner |
| **haiku** | together | 33.3s | 66 | 277597 | 4071 | $0.0805 |  |

### Quality Ranking (Opus 4.6)



## Content Quality

**haiku/baseline** is the most comprehensive answer, with accurate entity hierarchy, detailed relationship mappings with code snippets, domain logic coverage (Owner helper methods, Visit constructor, Vet sorted specialties), a thorough annotations summary table, and implicit schema discussion — though the "owners inherits from persons (single-table inheritance)" note is slightly misleading.

**haiku/together** is nearly as complete, with clean formatting, accurate relationship mappings, a useful relationship summary table, and correct code snippets with line references — slightly less detail on domain logic methods than baseline but well-organized.

**haiku/solo** provides solid coverage with accurate code and annotations, but includes some questionable details (e.g., Person marked as `@Entity @Table(name = "persons")` which may not be accurate depending on the actual codebase mapping strategy) and is slightly less polished in its summary sections.

## Efficiency

Solo is the clear efficiency winner at $0.046 and 23.8s, using far fewer input/cache tokens than the others. Together is the most expensive at $0.080 with the longest cache read, while baseline sits in the middle at $0.055. All three produce roughly similar quality output, making solo's ~45% cost savings over together significant.

## Verdict

**Winner: haiku/solo**

---

## js-express-lifecycle [javascript / hard]

> How does Express handle the full request/response lifecycle? Explain middleware chaining, how the Router works, how error-handling middleware differs from regular middleware, and how app.use and route mounting compose. Show key function signatures and flow.

### Time & Tokens

| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |
|-------|----------|----------|-----------|------------|------------|------------|--------|
| **haiku** | baseline | 92.4s | 18 | 50941 | 3929 | $0.1760 |  |
| **haiku** | solo | 75.6s | 202 | 1127339 | 6357 | $0.2477 |  |
| **haiku** | together | 53.5s | 100 | 541876 | 5023 | $0.1319 | 🏆 Winner |

### Quality Ranking (Opus 4.6)

## Content Quality

1. **haiku/together** — Most accurate source citations with correct line ranges (152-178, 190-244, 471-482) and code quotes that closely match the actual source; covers all asked topics (middleware chaining, Router, error handling, app.use/mounting) with a clean function signature summary table and correct `next()` pseudocode.

2. **haiku/baseline** — Impressively comprehensive with correct line references (152, 190, 256, 230-237, 109-122) and mostly accurate code quotes, though it embellishes in places (e.g., `flatten(arguments)` instead of the actual `flatten.call(slice.call(arguments, offset), Infinity)`, fabricated `app.get` shortcut code); the extensive req/res method listings go beyond what was asked but add value; some code is paraphrased rather than quoted verbatim.

3. **haiku/solo** — Correctly quotes `createApplication()` from express-express.js:36-56 but then drifts into fabricated pseudocode for Router/Layer internals that don't exist in the fixture files; the Router constructor/Layer code is speculative rather than sourced; covers the conceptual topics adequately but with less grounding in actual source.

## Efficiency

Together is the clear efficiency winner at $0.13 and 53.5s — roughly half the cost of solo ($0.25, 75.6s) and 75% the cost of baseline ($0.18, 92.4s). Solo consumed over 1.1M cache-read tokens for the least source-accurate answer, making it the worst cost-quality tradeoff.

## Verdict

**Winner: haiku/together**

---

## ruby-rails-middleware [ruby / hard]

> How does the Rails middleware stack work? Explain how Rack middleware is assembled, how ActionDispatch integrates, how requests flow through the stack, and how custom middleware is added. Show key classes, modules, and call signatures.

### Time & Tokens

| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |
|-------|----------|----------|-----------|------------|------------|------------|--------|
| **haiku** | baseline | 40.3s | 90 | 505333 | 3601 | $0.1176 |  |
| **haiku** | solo | 41.7s | 82 | 195681 | 3803 | $0.0777 | 🏆 Winner |
| **haiku** | together | 33.6s | 58 | 286292 | 3269 | $0.0945 |  |

### Quality Ranking (Opus 4.6)

## Content Quality

**haiku/solo** is the strongest answer — it covers all four requested areas with the most structured depth, includes accurate file/line references (engine.rb:515-523, metal.rb:18-63, metal.rb:315-327, metal.rb:249-255, application.rb:738-741), and uniquely explains the controller dispatch path and lazy evaluation. **haiku/together** is a close second — it adds Sinatra/Rack::Builder assembly details (sinatra-base.rb:1670-1676, 1584-1587, 1830-1832) that the others miss, providing broader context on how middleware tuples are stored and iterated, though it's slightly less focused on the Rails-specific path. **haiku/baseline** covers the same core concepts correctly but is the least detailed, with vaguer file references (application.rb:670 appears imprecise) and less depth on the build chain and controller dispatch flow.

## Efficiency

Solo is the cheapest at $0.078 with competitive runtime (41.7s), while baseline is the most expensive at $0.118 despite producing the weakest answer. Together sits in between at $0.094 and is the fastest at 33.6s. Solo offers the best quality-to-cost tradeoff by a clear margin.

## Verdict

**Winner: haiku/solo**

---

## rust-ripgrep-pipeline [rust / hard]

> How does ripgrep's search pipeline work end-to-end? Explain the searcher/matcher/sink architecture, how file walking is parallelized, how the Grep and Searcher types interact, and how results flow to the output layer. Show key traits, structs, and method signatures.

### Time & Tokens

| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |
|-------|----------|----------|-----------|------------|------------|------------|--------|
| **haiku** | baseline | 83.6s | 18 | 52879 | 2257 | $0.2001 |  |
| **haiku** | solo | 39.3s | 58 | 136867 | 4511 | $0.0782 | 🏆 Winner |
| **haiku** | together | 51.5s | 106 | 601490 | 4826 | $0.1355 |  |

### Quality Ranking (Opus 4.6)

All three answers cover the same core architecture with similar structure and accuracy. Let me check the actual fixture to assess correctness.

**haiku/solo** is the most detailed and best-organized answer, with specific file:line references (e.g., `rg-matcher-lib.rs:546-648`, `rg-search.rs:193`, `rg-search.rs:380-412`), accurate method signatures, a clear concurrency guarantees section, and a dedicated table explaining parallel patterns.

**haiku/together** is comparably thorough with good file:line references and a well-structured walkthrough, though it's slightly more verbose and repeats some information across sections; its coverage of `find_candidate_line` and binary detection adds useful detail.

**haiku/baseline** is reasonable but lacks file:line references entirely, contains some inaccuracies in method signatures (e.g., the `Sink` trait methods shown don't match the actual crate API precisely), and is less specific about the actual code structure.

## Efficiency

haiku/solo is the clear efficiency winner: fastest runtime (39.3s), lowest cost ($0.08), and moderate token usage while producing the highest-quality answer. haiku/baseline costs 2.5× more ($0.20) for a weaker answer, while haiku/together sits in between at $0.14 with comparable quality to solo but higher cost and longer runtime.

## Verdict

**Winner: haiku/solo**

---

## php-laravel-container [php / hard]

> How does the Laravel service container resolve dependencies? Explain binding, contextual binding, automatic injection, how the container builds concrete classes, and how service providers register bindings. Show key classes, interfaces, and method signatures.

### Time & Tokens

| Model | Scenario | Duration | Input Tok | Cache Read | Output Tok | Cost (USD) | Winner |
|-------|----------|----------|-----------|------------|------------|------------|--------|
| **haiku** | baseline | 46.4s | 66 | 283410 | 5380 | $0.0875 |  |
| **haiku** | solo | 53.8s | 122 | 338681 | 6019 | $0.1045 | 🏆 Winner |
| **haiku** | together | 55.0s | 1263 | 448752 | 5894 | $0.1088 |  |

### Quality Ranking (Opus 4.6)

## Content Quality

**haiku/solo** — Best answer. Provides accurate code with specific file:line references (Container.php:278-308, Container.php:943-1008, etc.), covers all five requested topics thoroughly, and includes a clean method signature summary table. The resolution flow summary is clear and well-structured.

**haiku/together** — Nearly identical quality to solo, with the same file:line references and code snippets. Adds a useful ASCII resolution flow diagram and explicitly covers `resolvePrimitive()` and circular dependency detection. Slightly more verbose but no more accurate.

**haiku/baseline** — Correct and comprehensive but lacks any file:line references, presenting code as if recalled from memory rather than sourced from the codebase. The `resolve()` method includes a `$needsContextualBuild` variable used before being defined, suggesting some reconstruction rather than direct reading. Still covers all topics well.

## Efficiency

Baseline is the cheapest ($0.087) and fastest (46.4s), while solo and together are close in cost ($0.104 vs $0.109) and runtime (~54-55s). Solo delivers the best quality-to-cost tradeoff: it matches together's quality with specific source references while using fewer input tokens (338K vs 449K cache reads) and costing slightly less.

## Verdict

**Winner: haiku/solo**

---

## Overall: Algorithm Comparison

| Question | Language | Difficulty | 🏆 Winner | Runner-up |
|----------|----------|------------|-----------|-----------|
| go-registry-concurrency | go | hard | haiku/solo | haiku/together |
| py-django-queryset | python | hard | haiku/solo | haiku/baseline |
| ts-disposable-events | typescript | hard | haiku/together | haiku/solo |
| java-petclinic-domain | java | hard | haiku/solo | haiku/baseline |
| js-express-lifecycle | javascript | hard | haiku/together | haiku/baseline |
| ruby-rails-middleware | ruby | hard | haiku/solo | haiku/together |
| rust-ripgrep-pipeline | rust | hard | haiku/solo | haiku/together |
| php-laravel-container | php | hard | haiku/solo | haiku/baseline |

**Scenario Win Counts** (across all 8 questions):

| Scenario | Wins |
|----------|------|
| baseline | 0 |
| solo | 6 |
| together | 2 |

**Overall winner: solo** — won 6 of 8 questions.

_Full answers and detailed analysis: `detail-report.md`_
