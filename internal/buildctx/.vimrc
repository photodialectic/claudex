" Don't write any backup or swap files ever.

set noswapfile
set nowritebackup
set nobackup

" colors
syntax on
set encoding=utf-8

" spaces & tabs
set tabstop=4
set shiftwidth=4

" js files are two spaces...
autocmd Filetype javascript setlocal tabstop=2 shiftwidth=2
autocmd Filetype javascriptreact setlocal tabstop=2 shiftwidth=2
autocmd Filetype yaml setlocal tabstop=2 shiftwidth=2
set expandtab
match ErrorMsg '\s\+$'
nnoremap <Leader>W :%s/\s\+$//e<CR>

" python pep8
au BufNewFile,BufRead *.py
    \set tabstop=4
    \set softtabstop=4
    \set shiftwidth=4
    \set textwidth=79
    \set expandtab
    \set autoindent
let python_highlight_all=1

" But wrap text for txt/markdown
autocmd FileType markdown set wrap linebreak textwidth=0
autocmd FileType txt set wrap linebreak textwidth=0

" But not for txt/markdown
autocmd FileType markdown set showbreak=
autocmd FileType txt set showbreak=

" ui config
set number              " show line numbers
set showcmd             " show command in bottom bar
set wildmenu            " visual autocomplete for command menu
set ruler
set backspace=indent,eol,start

" Searching
set incsearch ignorecase smartcase hlsearch " search as characters are entered

" More reasonable scroll keys
map J <c-e>
map K <c-y>

" buffer nav
nnoremap <Tab> :bnext<CR>
nnoremap <S-Tab> :bprevious<CR>
nnoremap <leader><leader> <c-^>

" Turn off arrow to be a better person
map <up> <nop>
map <down> <nop>
map <left> <nop>
map <right> <nop>

" leader shortcuts
" " jk is escape
inoremap jk <esc>
" " save quicker
nnoremap <leader>w :w<CR>
" " turn off search highlight
nnoremap <leader><space> :nohlsearch<CR>

nnoremap <leader>v :set paste<CR>
nnoremap <leader>V :set nopaste<CR>

noremap <C-a> <Nop>

filetype plugin indent on
