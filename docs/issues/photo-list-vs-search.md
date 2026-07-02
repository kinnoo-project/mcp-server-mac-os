**issue**

When the model is asked something like "Do I have any recent photos (taken in the
last 2 days) in my Photos library?", it reaches for the `list_photos` operation
and paginates, when `search_photos` would be the better tool: `search_photos`
runs against the Photos index and is much faster than listing and filtering the
whole library client-side.

The operation descriptions in the `application-photos` manifest should steer a
date-bounded or otherwise filterable request toward `search_photos`, reserving
`list_photos` for the "enumerate everything" case. This is a manifest-wording
issue, not a defect in either operation.
