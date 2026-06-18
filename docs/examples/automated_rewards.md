# Automated Rewards

```vibe
# vibe: 0.4
# uses: db, jobs

def reward_for_total(total)
  if total >= money("500.00 USD")
    "gold"
  elsif total >= money("250.00 USD")
    "silver"
  else
    nil
  end
end

def schedule_reward_checks
  players = db.query("Player", where: { status: "active" })
  players.each do |player|
    reward = reward_for_total(player[:raised])

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
