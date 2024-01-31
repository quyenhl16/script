###### ----- Sed Command in Linux ---- #######
# ---- 1. Delete a line in file ------- #
#---Ex1.1: delete line number 2 in file abc.txt ----#
sed -i "2d" abc.txt
#---Ex1.2: delete all line in file abc.txt -----#
sed -i "%d" abc.txt

# ---- 2. Replace a string in file ------- #

#---Ex2.1: Replace string "abc" by "xyz" in file abc.txt ----#
sed -i 's/abc/xyz/g' abc.txt
#*Note: '/' using to delimiter, we can also using other character like: '@'; '+', '\'
#---Ex2.1: Replace string "abc" by variable $r in file abc.txt ----#
sed -i "s/abc/$r/g" abc.txt
#*Note: If uses varriable, we need use double quote "".

# ---- 3. Replace value in yaml file ------- #
#---Ex3.1: Replace value in key "field" by "xyz" in file abc.yaml ----#
sed -i 's/field: .*/field: xyz/g' abc.yaml
#*Note: If uses varriable, we need use double quote "".
