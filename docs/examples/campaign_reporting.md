# Campaign Reporting

```vibe
# vibe: 0.2
# uses: db, events

def daily_summary(campaign_id)
  rows = []
  db.each("Donation", where: { campaign_id: campaign_id }) do |donation|
    rows = rows.push({
      supporter: donation[:supporter_name],
      amount: donation[:amount],
      received_at: donation[:received_at]
    })
  end

  rows = rows.sort_by do |row|
    row[:received_at]
  end

  totals = rows.reduce({ supporters: [], total: money("0.00 USD") }) do |state, row|
    state[:supporters] = (state[:supporters] + [row[:supporter]]).uniq
    state[:total] = state[:total] + row[:amount]
    state
  end

  formatted_rows = rows.map do |row|
    {
      supporter: row[:supporter],
      amount: row[:amount].format,
      received_at: row[:received_at]
    }
  end

  events.publish("campaign_reports", {
    campaign_id: campaign_id,
    supporters: totals[:supporters],
    total: totals[:total].format,
    rows: formatted_rows
  })
end
```
