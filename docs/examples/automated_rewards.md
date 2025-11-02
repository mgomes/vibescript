# Automated Rewards

```vibe
# vibe: 0.2
# uses: db, jobs

def schedule_reward_checks()
  players = db.query("Player", where: { status: "active" })
  players.each do |player|
    total = player[:raised]
    reward = case total
             when total >= money("500.00 USD")
               "gold"
             when total >= money("250.00 USD")
               "silver"
             else
               nil
             end

    if reward != nil
      jobs.enqueue(
        "send_reward_email",
        {
          player_id: player[:id],
          reward: reward
        },
        key: "reward:" + player[:id]
      )
    end
  end
end
```
