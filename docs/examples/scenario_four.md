# Scenario 4: Leaderboard Updates

```vibe
# vibe: 0.2
# uses: db, events

def refresh_leaderboard(limit)
  top_players = db.query("Player", order: { raised: :desc }, limit: limit)
  players = top_players.map do |player|
    {
      id: player[:id],
      name: player[:name],
      raised: player[:raised].format()
    }
  end

  events.publish("leaderboard", {
    players: players
  })
end
```
