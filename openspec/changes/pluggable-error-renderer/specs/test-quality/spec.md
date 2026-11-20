# Spec Delta

## ADDED Requirements

### Requirement: The error renderer path is benchmarked against its baseline
The error path and the happy path SHALL be benchmarked on every engine (`ns/op`, `B/op`, `allocs/op`) for the change's base commit, the default renderer and a trivial custom renderer, compared with `benchstat` over 10 interleaved runs on one machine. A default-renderer regression over 5% `ns/op` on any error row, or any extra `allocs/op` on the success row, SHALL stop the work for an owner decision.

#### Scenario: Renderer benchmark comparison
- **Given** `BenchmarkPipeline` row `<row>` on engine `<engine>` measured at the base commit and with `<renderer>`
- **When** `benchstat` compares the two sets of 10 interleaved runs
- **Then** the result is `<result>`

##### Examples:
| row | engine | renderer | result |
|-----|--------|----------|--------|
| `success` | `net/http` | default | passes with the same `allocs/op` as the base |
| `success` | `gin` | default | passes with the same `allocs/op` as the base |
| `success` | `fiber` | default | passes with the same `allocs/op` as the base |
| `success` | `net/http` | default, one more `allocs/op` than the base | stops for the owner, naming the row and both values |
| `handler_error` | every engine | default, `ns/op` within 5% of the base | passes |
| `handler_error` | `gin` | default, `ns/op` 6% over the base with `p < 0.05` | stops for the owner, naming the row and both values |
| `handler_error` | `fiber` | default, `ns/op` 6% over the base with `p ≥ 0.05` (`~`) | passes, reported as not significant |
| `not_found` and `method_not_allowed` | every engine | default | passes within 5% `ns/op` |
| `panic` | every engine | default | passes within 5% `ns/op` |
| `handler_error`, `not_found`, `method_not_allowed`, `panic` | every engine | trivial custom renderer | recorded for information; no threshold |
| any row | any engine | a run while another benchmark session uses the machine | not run: the owner is asked first and the numbers are discarded |
