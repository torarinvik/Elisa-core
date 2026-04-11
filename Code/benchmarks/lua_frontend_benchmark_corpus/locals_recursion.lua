local function fact(n)
    if n == 0 then
        return 1
    end
    return fact(n - 1)
end

local acc = 0
for i = 1, 8, 1 do
    acc = acc + i
end

return fact, acc
