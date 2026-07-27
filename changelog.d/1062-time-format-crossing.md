- **Fixed: crossing `Time#format` and `Time#strftime` returned the format string
  as data.** `t.strftime("2006-01-02")` answered `"2006-01-02"` -- a string that
  looks exactly like a formatted date, because the Go reference layout is one, so
  a report could emit the same twenty-year-old date on every row and pass a
  visual check, a type check, and an ISO-8601 regex. Both directions now report
  which format language they expected and which method takes the one supplied.
