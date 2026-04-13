local seed = 7

local function make_pipeline(offset)
    local running = offset + seed
    local stages = {}
    for i = 1, 4 do
        local step = i
        stages[i] = function(value)
            local inner = function(extra)
                return value + extra + running + step
            end
            return inner(step)
        end
    end

    return function(base)
        local total = base
        for i = 1, 4 do
            total = total + stages[i](i)
        end
        return total + running
    end
end

local pipeline = make_pipeline(3)
return pipeline, make_pipeline
