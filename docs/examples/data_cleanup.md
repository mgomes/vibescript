# Data Cleanup

```vibe
# vibe: 0.2
# uses: db

def normalize_players()
  players = []
  db.each("Player") do |player|
    players.push(player)
  end

  updates = players.map do |player|
    normalized = {
      id: player[:id],
      name: player[:name].strip(),
      email: player[:email].downcase(),
      raised: player[:raised]
    }

    if normalized[:name] == ""
      normalized[:name] = "Anonymous"
    end

    normalized
  end

  updates.each do |player|
    db.update("Player", player[:id], player)
  end

  updates
end
```
