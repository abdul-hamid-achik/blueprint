" Indentation for Blueprint (.bp)
if exists("b:did_indent")
  finish
endif
let b:did_indent = 1

setlocal autoindent
setlocal indentexpr=GetBpIndent()
setlocal indentkeys+=0},0)

if exists("*GetBpIndent")
  finish
endif

function! GetBpIndent()
  let lnum = prevnonblank(v:lnum - 1)
  if lnum == 0
    return 0
  endif

  let prevline = getline(lnum)
  let curline  = getline(v:lnum)
  let ind      = indent(lnum)

  " Increase indent after opening brace or bracket
  if prevline =~# '[{[]\s*$'
    let ind += shiftwidth()
  endif

  " Decrease indent for closing brace or bracket
  if curline =~# '^\s*[}\]]'
    let ind -= shiftwidth()
  endif

  return ind
endfunction
