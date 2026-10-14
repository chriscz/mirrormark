# Working conventions

AI-assisted work is welcome here, under one rule: a human owns every change.
Every AI-assisted commit and PR carries a `Co-Authored-By:` line.

## Principles

Changes should be minimally intrusive and build on what is already there.

- **A human must own every PR.** Even when AI helped, a person signs off that
  the work is theirs, that they reviewed and tested it, and that they'll put
  their name on it, in the spirit of the Developer Certificate of Origin.
- **Prefer small, independent PRs** that each take one step, over one large
  change.
- **Test thoroughly before sending a PR.**
- **Reuse what exists before adding anything.** Find the existing code that
  does something similar and extend it. Don't build a parallel path.
- **Generate derived files rather than duplicating them**, so a change to the
  source carries over without anyone remembering to update a second file.
- **One build system.** Use and tweak the `Makefile`. Don't add separate build
  scripts or systems.
- **Use the existing helpers.**
  - Reuse existing feature checks and helpers instead of re-checking inline.
  - Extend existing per-browser or per-platform branches rather than adding
    new ones.
  - If something underneath is wrong, fix it at the source instead of working
    around it in the caller.
- **No escape hatches from the type system.** No `any`/`interface{}` or
  unchecked type assertions to dodge a missing type; define or fix the type
  instead.
- **Don't weaken safety for convenience.** This project edits people's
  bookmarks. Any change that could lose or corrupt user data needs evidence
  that the data stays safe.
- **Keep diagnostics.** Don't remove warnings just because one browser or
  platform triggers them more often.
- **Don't duplicate docs.** Check that the README doesn't already say it.
- **Expect to address review comments.** Respond to review rather than letting
  a PR go stale.

## Commits

- Use [Conventional Commits](https://www.conventionalcommits.org/):
  `type(scope): summary`.
- Work that belongs to an RFC uses the RFC as its scope, e.g.
  `feat(rfc-0001): accept browser connections over JSON` or
  `docs(rfc-0001): update proposal after review`. This shows which RFC each
  commit serves.
- A fix for a bug that already existed before the current work ends its
  subject with `[pre-existing]`, e.g.
  `fix(rfc-0002): keep folder order when merging [pre-existing]`. If the fix
  changes what the user sees, it comes with an end-to-end test; otherwise a
  unit regression test.
- New functionality, as opposed to fixes and compatibility changes, ends its
  subject with `[new feature]`.
- Build commits as a Gerrit-style stack: each commit is one coherent change that
  builds, passes `make`, and delivers value on its own. Order the stack so any
  prefix of it could land without the rest.

## RFC process

1. **Draft** the RFC in `docs/rfcs/NNNN_<slug>.md` with the `rfc-craft` skills.
   It starts as `draft`.
2. **Review** it with the independent review wave from `writing-rfc-prose`,
   integrate the findings, and commit it as `docs(rfc-NNNN): ...`.
3. **Plan** with `superpowers:writing-plans`, using the RFC as the spec. The plan
   lives at `docs/rfcs/NNNN_<slug>.d/plan.md`.
4. **Implement** with `superpowers:subagent-driven-development`. This is the
   default for all implementation unless stated otherwise.
5. **Keep the RFC true.** The RFC stays `draft` until its implementation lands.
   If the plan or implementation changes anything the RFC says, update the RFC
   in the same stack, in its own `docs(rfc-NNNN): ...` commit.
6. **Close out** with `rfc-craft`: move the RFC to `in review`, then `accepted`,
   once the implementation is merged.

**Track bugs outside the current RFC in `TASKS.md`.** When work turns up a bug
that isn't the current RFC's job, record it there (where, what, effect, how
it was found, the fix) instead of fixing it in passing.

**Never reference RFCs in code.** No RFC numbers, section references such as
`§2a`, or links to `docs/rfcs/` in source, comments, tests or build files.
Comments explain why in their own words; the commit scope (`rfc-NNNN`) carries
the link to the RFC.

## RFCs

RFCs live in [docs/rfcs/](docs/rfcs/).

<!-- rfc-index:start -->

| #   | Title | Status |
| --- | ----- | ------ |

<!-- rfc-index:end -->
