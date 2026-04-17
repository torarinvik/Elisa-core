local function add_pair(t)
    return t.answer + t[1]
end

local obj = {
    answer = 40,
    [1] = 2,
    send = function(self, value)
        return self.answer + self[1] + value
    end,
}

obj.answer = obj.answer + 1
return add_pair(obj) + obj:send(3)