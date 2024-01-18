#!/bin/bash


# ## Generate a.txt

grep -o 'UL: [^ S5]* S5' A.txt | sed 's/UL: //;s/ S5//' > a.txt

## Generate b.txt

grep -o '\[[^]]*\]' B.txt | sed 's/\[\(.*\)\]/\1/' > b.txt

# Search a in b

# sort and uniq a.txt

(sort a.txt | uniq ) > A1.txt 

# # sort and uniq b.txt 

(sort b.txt | uniq ) > B1.txt

comm -12 A1.txt B1.txt > res.txt
