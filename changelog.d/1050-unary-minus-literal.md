- **Fixed: a minus before a numeric literal binds to the literal.** `-5.abs`
  returned `-5` because the sign bound looser than the member call and landed on
  the method's result, and `-5.to_s` failed outright. The sign now folds into the
  literal, matching Ruby, while `-2 ** 2` keeps its `-4` value and `-x.abs` on a
  variable still means `-(x.abs)`.
