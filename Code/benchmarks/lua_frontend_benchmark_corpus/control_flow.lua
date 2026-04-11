global cond, flag, done
local total = 0
while cond do
    repeat
        if flag then
            break
        end
    until done
    total = total + 1
end
return total
