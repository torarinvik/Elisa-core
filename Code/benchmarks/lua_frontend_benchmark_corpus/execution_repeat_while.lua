local total = 0
local current = 5

repeat
    total = total + current
    current = current - 1
until current == 0

local adjust = 0
while adjust < 3 do
    total = total + (adjust * 2)
    adjust = adjust + 1
end

return total