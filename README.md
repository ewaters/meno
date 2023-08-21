# meno

## About

Meno was created as an alternative to the standard pager `less` with only one
goal: to make searching in the file faster.

If you view with `less` a very long file, jumping to the first occurrence of a
particular string can take a very long time. `meno` seeks to make this jump as
fast as possible, at the expense of CPU and memory, with the assumption that you
probably have a lot of both and are willing to trade it for faster searching.

## Install

Assuming you have a $USER/bin directory:

```bash
git checkout https://github.com/ewaters/meno.git
cd meno
git build -o ~/bin/meno *.go
```

## Usage

```bash
meno <large file>
```

You have the following keyboard shortcuts in the pager:

- 'g'/'G': Go to first/last line in file
- 'b'/PgUp: Go up a page
- 'f'/Space/PgDown: Go down a page
- 'j'/ArrowDown: Go down a line
- 'k'/ArrowUp: Go up a line
- 'q'/CtrlC: Quit

But the most important ones are:

- '/': Find down
- '?': Find up
- 'n': (after a search) Next result
- 'N': (after a search) Previous result
