# Week 2 check-in

## Completed

- Teacher feedback: separate images, jobs, and variants up/down migrations; pushed first as commit `652b768`.
- Atomic image + queued-job acceptance and correct 202/Location contract.
- Job resource, exactly one worker, three transformations and variant retrieval.
- Processing/completed/failed timestamps, safe failure message, all-or-complete metadata transaction.
- Automated PostgreSQL success and deliberately induced failure demonstration.

## Evidence

Run the README commands and `TestWeek2Integration`. The test observes queued and processing before completed, decodes every served variant to compare actual dimensions, and removes a test original to demonstrate failed. Unit tests cover landscape, portrait, small-image dimensions, and center cropping.

## Incomplete / next

Week 3: automatic one-second browser polling, timeline/results rendering, cancellation, and retrieval-error recovery. Week 4: full acceptance evidence and measurement experiments. A live browser/Network-panel presentation is still needed at the check-in.

## Blocker

None for the Week 2 server implementation. Local startup requires your PostgreSQL DSN.

## Concept explanation

The POST stores the original and commits an image and queued job before returning 202. It does not generate variants. A single background loop claims durable jobs from PostgreSQL, so work exists independently of the browser or request. One worker processes jobs serially, which makes later jobs wait. Completion means all three files and their metadata are available; 202 alone does not promise success.

## Question for the teacher

Is documenting jobs left in processing after an abrupt crash sufficient for Version 1, given automatic retries are out of scope?
