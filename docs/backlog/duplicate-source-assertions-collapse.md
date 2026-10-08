---
worth: yes
where: internal/schema/schema.go:103
added: 2026-10-08
---
# duplicate-source assertions silently collapse into one

In the expression form of `assert:`, several lines on the same source keep only the last one,
and nothing reports that the others were dropped:

```yaml
assert:
  - "$.body.keys() contains partners"   # never evaluated
  - "$.body.keys() contains meta"
```

`Assert.Body` and `Assert.Headers` are `map[string]Assertion` keyed by source, and `Assert.Status`
is a single pointer. A range check like `status >= 200` plus `status < 300` therefore loses its
lower bound. The mapping form can't express two checks on one source at all, because YAML keys are
unique.

This was found while adding `.keys()`, where several `contains` checks on one source are the natural
thing to write. The fix is to store a list of assertions per source, which touches schema unmarshalling,
`assert.Evaluate` and the output formatters. The cheapest step would be a load-time error on a
duplicate source, so the loss at least isn't silent.
