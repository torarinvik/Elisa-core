-- lexer-heavy accepted sample with comments, long strings, escapes, and mixed numerics
local banner = [=[
[[ lexer stress ]]
line two
]=]

local escaped = "path:\\tmp\\lua\nline\tindent\x41\123\z   end"
local stats = {
    hex_mask = 0xff,
    hex_float = 0x1.fp+2,
    decimal_float = 12.5e-1,
    shifted = (1 << 4) | (~3 & 7),
}

local names = { "alpha", 'beta', banner, escaped }
local idx = 1
while idx <= 4 do
    names[idx] = names[idx] .. "::" .. idx
    idx = idx + 1
end

return banner, escaped, stats, names
