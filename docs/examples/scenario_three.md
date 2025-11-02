# Scenario 3: Auto-Renewal Reminder

```vibe
# vibe: 0.2
# uses: db, jobs

def schedule_auto_renewal_reminders()
  db.each("Subscription", where: { status: :active }) do |subscription|
    renewal_date = subscription[:renews_at]
    reminder_time = renewal_date - 3.days

    jobs.enqueue(
      "send_auto_renewal_email",
      {
        subscription_id: subscription[:id],
        supporter_id: subscription[:supporter_id],
        renews_at: renewal_date,
      },
      key: "subscription:renewal:" + subscription[:id],
      run_at: reminder_time,
    )
  end
end
```
