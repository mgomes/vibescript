- **Improved: a nullable shape-field read says why.** Reading a required field
  from a shape-typed parameter types as `T | nil`, because a shape accepts
  either key kind — `{name: "a"}`, `{"name": "b"}`, and `JSON.parse` output all
  satisfy `{ name: string }` — so the checker cannot know which one the read
  hits. The bare diagnostic read as though the field were optional. It now
  names the real cause, and only where that is the cause.
