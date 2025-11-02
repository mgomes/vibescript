# Scenario 2: High-Value Notifications

```vibe
# vibe: 0.2
# uses: db, events

def notify_high_value_donations(threshold)
  db.each("Donation") do |donation|
    if donation[:amount] >= threshold
      events.publish("high_value_donations", {
        donation_id: donation[:id],
        supporter: donation[:supporter_name],
        amount: donation[:amount].format(),
        campaign_id: donation[:campaign_id],
      })
    end
  end
end
```
