" Vim syntax file for Blueprint (.bp)
" Language:    Blueprint
" Maintainer:  Blueprint contributors
" URL:         https://github.com/abdul-hamid-achik/blueprint

if exists("b:current_syntax")
  finish
endif

" ---- Comments ----
syn match  bpComment  "#.*$"               contains=bpTodo
syn keyword bpTodo    TODO FIXME NOTE HACK contained

" ---- Arrows — the core of Blueprint syntax ----
" Order matters: @> must come before @
syn match bpLLMSlot    "@>"
syn match bpIntent     "@\ze\([^>]\|$\)"
syn match bpInputArrow "<-"
syn match bpStepArrow  "|>"
syn match bpOutputArrow "->"

" ---- String literals with {interpolation} ----
syn region bpString        start='"' end='"' contains=bpInterpolation oneline
syn region bpInterpolation start='{' end='}' contained

" ---- HTTP methods ----
syn keyword bpHttpMethod GET POST PUT PATCH DELETE STREAM WS

" ---- Top-level declaration keywords ----
syn keyword bpDecl
      \ blueprint model fn pipe middleware worker schedule
      \ secret env type alias enum include external subscribe
      \ test fixture test_group

" ---- Block section keywords ----
syn keyword bpSection
      \ before after stream logic impl setup request expect cleanup
      \ on_error on_fail on_connect on_message on_disconnect

" ---- Control flow ----
syn keyword bpControl guard when try recover skip

" ---- Real-time / streaming ----
syn keyword bpRealtime close join leave broadcast whisper

" ---- Data operations ----
syn keyword bpDataOp
      \ fetch query save update delete count upload download
      \ emit call log map seed inject pipe

" ---- Built-in field types ----
syn keyword bpType
      \ string int float bool uuid timestamp json file money

" ---- Field constraints ----
syn keyword bpConstraint
      \ required optional primary unique index default ref format min max auto

" ---- Query modifiers ----
syn keyword bpModifier
      \ where paginate order first asc desc

" ---- Block config keys ----
syn keyword bpConfig
      \ version port runtime database cache storage queue
      \ cron retry timeout trigger limit tags auth use

" ---- Logical and relational operators (keyword form) ----
syn keyword bpOperator and or not in from as with using

" ---- Boolean / null / time literals ----
syn keyword bpLiteral true false null now

" ---- Runtime / backend identifiers ----
syn keyword bpRuntime
      \ node postgres redis s3 mysql sqlite mongo memcached
      \ sqs rabbitmq gcs local bearer api_key session webhook_sig

" ---- MIME types: image/png, application/pdf, video/*, etc. ----
syn match bpMime "\v(image|video|audio|text|application)\/(\*|[-+\w]+)"

" ---- Numeric literals with units ----
" Rate: 60/min, 1000/hour
syn match bpRate   "\v<\d+\/(min|hour|day)>"
" Duration / size: 500ms, 10mb, 30days, 1hour
syn match bpNumber "\v<\d+(ms|s|min|hour|hours|day|days|kb|mb|gb|b)>"
" Plain integer or float
syn match bpNumber "\v<\d+(\.\d+)?>"

" ---- Path parameters: :id, :user_id ----
syn match bpPathParam ":\w\+"

" ---- Highlight linking ----
hi def link bpComment      Comment
hi def link bpTodo         Todo

hi def link bpLLMSlot      PreProc
hi def link bpIntent       PreProc

hi def link bpInputArrow   Special
hi def link bpStepArrow    Operator
hi def link bpOutputArrow  Keyword

hi def link bpString       String
hi def link bpInterpolation Identifier

hi def link bpHttpMethod   Function

hi def link bpDecl         Keyword
hi def link bpSection      Keyword
hi def link bpControl      Conditional
hi def link bpRealtime     Function
hi def link bpDataOp       Function

hi def link bpType         Type
hi def link bpConstraint   StorageClass
hi def link bpModifier     StorageClass

hi def link bpConfig       Identifier
hi def link bpOperator     Operator
hi def link bpLiteral      Boolean
hi def link bpRuntime      Constant

hi def link bpMime         Constant
hi def link bpRate         Number
hi def link bpNumber       Number
hi def link bpPathParam    Identifier

let b:current_syntax = "bp"
