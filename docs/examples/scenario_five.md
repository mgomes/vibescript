# Scenario 5: Donation Upsell

```vibe
# vibe: 0.2
# uses: db, jobs

def schedule_upsell_followups
  db.each("Donation", where: { status: :processed }) do |donation|
    if donation[:amount] < money("50.00 USD")
      jobs.enqueue(
        "upsell_followup",
        {
          donation_id: donation[:id],
          supporter_id: donation[:supporter_id],
          amount: donation[:amount]
        },
        delay: 3.days,
        key: "upsell:" + donation[:id]
      )
    end
  end
end
```
