- **Fixed: scaling a duration by a fraction no longer collapses it to zero.**
  `1.hour * 0.5` returned `0s` because a float operand was truncated to an
  integer before scaling, so every factor below one produced a zero duration and
  `1.hour / 0.5` reported a division by zero. Float factors and divisors now
  scale properly and round to the nearest second; integer operands keep their
  exact path.
