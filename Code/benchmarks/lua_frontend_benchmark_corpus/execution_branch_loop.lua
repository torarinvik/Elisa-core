local total = 0

for i = 1, 6 do
    if i % 2 == 0 then
        total = total + (i * 3)
    else
        total = total + (i - 1)
    end
end

if total > 30 then
    total = total - 5
end

return total