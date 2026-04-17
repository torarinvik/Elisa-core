local function fact(n)
    if n <= 1 then
        return 1
    end
    return n * fact(n - 1)
end

local function make_adder(base)
    return function(value)
        return base + value
    end
end

local add7 = make_adder(7)
return fact(5) + add7(3)