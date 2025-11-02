# Scenario 1: Instant Payouts

```vibe
# vibe: 0.2
# uses: db, payments

def process_payouts()
  db.each("Donation", where: { payout_status: :pending }) do |donation|
    response = payments.send({
      donation_id: donation[:id],
      amount: donation[:amount],
    })

    status = if response[:ok]
      :complete
    else
      :failed
    end

    db.update("Donation", donation[:id], { payout_status: status })
  end
end
```
