global foo
local x = 1
local function outer(n)
    local y = n
    local inner = function()
        return x + y + foo
    end
    return inner
end
return outer
