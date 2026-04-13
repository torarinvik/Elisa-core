local total = 0
local flag = false
local done = false

::outer::
for i = 1, 5 do
    local current = i
    repeat
        if current % 2 == 0 then
            total = total + current
            break
        end
        total = total + current + 1
        done = current > 3
    until done
    if done then
        goto finish
    end
end

while flag do
    total = total + 1
    if total > 40 then
        goto outer
    end
end

::finish::
return total
