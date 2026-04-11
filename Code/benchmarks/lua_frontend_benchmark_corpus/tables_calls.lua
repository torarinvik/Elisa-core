global f, obj
local first = f { answer = 42, [1] = "x" }
local second = obj:send "hello"
return first, second
