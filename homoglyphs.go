package certificate_searcher

import (
	"bufio"
	"encoding/json"
	"fmt"
	"golang.org/x/net/idna"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"
)

var GLYPH_TO_ASCII map[string][]rune
var ASCII_TO_GLYPH map[rune][]rune
var ASCII_HOMOGLYPHS []rune

func runesContain(runes []rune, target rune) bool {
	for _, r := range runes {
		if r == target {
			return true
		}
	}
	return false
}

func indexesContain(ints []int, target int) bool {
	for _, i := range ints {
		if i == target {
			return true
		}
	}
	return false
}

func findDomainCharIndexes(s string) []int {
	runes := []rune(s)
	idxs := make([]int, 0)
	for idx, r := range runes {
		if validDomainChar(r) {
			idxs = append(idxs, idx)
		}
	}

	//if len(idxs) > 1 {
	//	fmt.Printf("More than one valid domain character found in line: %s\n", s)
	//	for _, i := range idxs {
	//		fmt.Printf("%+q\n", runes[i])
	//	}
	//}

	return idxs
}

func validDomainChar(r rune) bool {
	if r == '.' || r == '-' {
		return true
	}
	if r >= '\u0030' && r <= '\u0039' {
		return true
	}
	if r >= '\u0061' && r <= '\u007a' {
		return true
	}
	if r >= '\u0041' && r <= '\u005a' {
		return true
	}

	return false
}

func addStringAscii(key string, ascii rune) {
	ascii = unicode.ToLower(ascii)

	if ascii > unicode.MaxASCII {
		panic(fmt.Sprintf("Invalid ascii value %q", ascii))
	}

	if !validDomainChar(ascii) {
		return
	}

	if len(key) == 1 && validDomainChar(rune(key[0])) {
		ASCII_HOMOGLYPHS = append(ASCII_HOMOGLYPHS, rune(key[0]))
	}

	if _, ok := GLYPH_TO_ASCII[key]; !ok {
		GLYPH_TO_ASCII[key] = make([]rune, 0)
		GLYPH_TO_ASCII[key] = append(GLYPH_TO_ASCII[key], ascii)
	} else if !runesContain(GLYPH_TO_ASCII[key], ascii) {
		//fmt.Printf("Multiple mapping %s: %q and %q\n", key, ascii, GLYPH_TO_ASCII[key])
		GLYPH_TO_ASCII[key] = append(GLYPH_TO_ASCII[key], ascii)
	}
}

func addAsciiString(ascii rune, glyph rune) {
	ascii = unicode.ToLower(ascii)

	if ascii > unicode.MaxASCII {
		panic(fmt.Sprintf("Invalid ascii value %q", ascii))
	}

	if !validDomainChar(ascii) {
		return
	}
	if _, present := ASCII_TO_GLYPH[ascii]; !present {
		ASCII_TO_GLYPH[ascii] = make([]rune, 0)
		ASCII_TO_GLYPH[ascii] = append(ASCII_TO_GLYPH[ascii], glyph)
	} else if !runesContain(ASCII_TO_GLYPH[ascii], glyph) {
		ASCII_TO_GLYPH[ascii] = append(ASCII_TO_GLYPH[ascii], glyph)
	}
}

func init() {
	GLYPH_TO_ASCII = make(map[string][]rune)
	ASCII_TO_GLYPH = make(map[rune][]rune)
	ASCII_HOMOGLYPHS = make([]rune, 0)
	// https://github.com/reinderien/mimic/blob/master/mimic/__init__.py
	// Map of all homoglyphs - named tuples with 'ascii' char, 'fwd' alternatives string for forward mimic mode, and 'rev'
	// string of potentially non-universally-printable chars that should still be able to check or reverse back to ASCII

	mimicHomoglyphs := map[rune][]string{
		' ':  {"\u00A0\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200A\u202F\u205F", "\u3000"},
		'!':  {"\uFF01\u01C3\u2D51\uFE15\uFE57", "\u119D"},
		'"':  {"\uFF02", "\u030E\u17C9"},
		'#':  {"\uFF03\uFE5F", ""},
		'$':  {"\uFF04\uFE69", ""},
		'%':  {"\uFF05\u066A\u2052\uFE6A", ""},
		'&':  {"\uFF06\uFE60", ""},
		'\'': {"\uFF07\u02B9\u0374", "\u030D"},
		'(':  {"\uFF08\uFE59", "\u207D\u208D\u27EE"},
		')':  {"\uFF09\uFE5A", "\u207E\u208E\u27EF"},
		'*':  {"\uFF0A\u22C6\uFE61", ""},
		'+':  {"\uFF0B\u16ED\uFE62", "\u207A\u208A"},
		',':  {"\uFF0C\u02CF\u16E7\u201A", "\uFE10\uFE50\u0317\u0326"},
		'-':  {"\uFF0D\u02D7\u2212\u23BC\u2574\uFE63", "\u207B\u208B\u0335\u1680\u174D\u1806\u1C7C\u23AF\u2CBB\u30FC\u3127"},
		'.':  {"\uFF0E\u2024", "\uFE52\u0323"},
		'/':  {"\uFF0F\u1735\u2044\u2215\u29F8", "\u0338\u2CC6\u3033"},
		'0':  {"", "\u2070\u2080\u24EA\uFF10\u1C50"},
		'1':  {"", "\u00B9\u2081\u2460\uFF11"},
		'2':  {"\u14BF", "\u00B2\u2082\u2461\uFF12"},
		'3':  {"\u01B7\u2128", "\u00B3\u2083\u2462\uFF13\u1883\u2CC4\u2CCC\u2CCD"},
		'4':  {"\u13CE", "\u2074\u2084\u2463\uFF14"},
		'5':  {"", "\u2075\u2085\u2464\uFF15"},
		'6':  {"\u13EE", "\u2076\u2086\u2465\uFF16"},
		'7':  {"", "\u2077\u2087\u2466\uFF17"},
		'8':  {"", "\u2078\u2088\u2467\uFF18"},
		'9':  {"\u13ED", "\u2079\u2089\u2468\uFF19"},
		':':  {"\uFF1A\u02D0\u02F8\u0589\u1361\u16EC\u205A\u2236\u2806\uFE13\uFE55", "\u05C3\u17C7\u17C8\u1804\u1C7A\uFE30"},
		';':  {"\uFF1B\u037E\uFE14\uFE54", ""},
		'<':  {"\uFF1C\u02C2\u2039\u227A\u276E\u2D66\uFE64", "\u3031\u3111"},
		'=':  {"\uFF1D\u2550\u268C\uFE66", "\u207C\u208C\u30A0"},
		'>':  {"\uFF1E\u02C3\u203A\u227B\u276F\uFE65", ""},
		'?':  {"\uFF1F\uFE16\uFE56", ""},
		'@':  {"\uFF20\uFE6B", ""},
		'A':  {"\u0391\u0410\u13AA", "\u1D2C\u24B6\uFF21"},
		'B':  {"\u0392\u0412\u13F4\u15F7\u2C82", "\u1D2E\u212C\u24B7\uFF22"},
		'C':  {"\u03F9\u0421\u13DF\u216D\u2CA4", "\u2102\u212D\u24B8\uFF23"},
		'D':  {"\u13A0\u15EA\u216E", "\u1D30\u2145\u24B9\uFF24"},
		'E':  {"\u0395\u0415\u13AC", "\u1D31\u2130\u24BA\uFF25"},
		'F':  {"\u15B4", "\u2131\u24BB\uFF26"},
		'G':  {"\u050C\u13C0", "\u1D33\u24BC\uFF27"},
		'H':  {"\u0397\u041D\u12D8\u13BB\u157C\u2C8E", "\u1D34\u210B\u210C\u210D\u24BD\uFF28"},
		'I':  {"\u0399\u0406\u2160", "\u1D35\u2110\u2111\u24BE\uFF29"},
		'J':  {"\u0408\u13AB\u148D", "\u1D36\u24BF\uFF2A"},
		'K':  {"\u039A\u13E6\u16D5\u212A\u2C94", "\u1D37\u24C0\uFF2B"},
		'L':  {"\u13DE\u14AA\u216C", "\u1D38\u2112\u24C1\uFF2C\u2CD0\u31C4"},
		'M':  {"\u039C\u03FA\u041C\u13B7\u216F", "\u1D39\u2133\u24C2\uFF2D"},
		'N':  {"\u039D\u2C9A", "\u1D3A\u2115\u24C3\uFF2E"},
		'O':  {"\u039F\u041E\u2C9E", "\u1D3C\u24C4\uFF2F\u1C5B"},
		'P':  {"\u03A1\u0420\u13E2\u2CA2", "\u1D3E\u2119\u24C5\uFF30"},
		'Q':  {"\u051A\u2D55", "\u211A\u24C6\uFF31\u10B3"},
		'R':  {"\u13A1\u13D2\u1587", "\u1D3F\u211B\u211C\u211D\u24C7\uFF32"},
		'S':  {"\u0405\u13DA", "\u24C8\uFF33\u10BD"},
		'T':  {"\u03A4\u0422\u13A2", "\u1D40\u24C9\uFF34"},
		'U':  {"", "\u1D41\u24CA\uFF35"},
		'V':  {"\u13D9\u2164", "\u24CB\uFF36"},
		'W':  {"\u13B3\u13D4", "\u1D42\u24CC\uFF37"},
		'X':  {"\u03A7\u0425\u2169\u2CAC", "\u24CD\uFF38"},
		'Y':  {"\u03A5\u2CA8", "\u03D2\u24CE\uFF39"},
		'Z':  {"\u0396\u13C3", "\u2124\u24CF\uFF3A"},
		'[':  {"\uFF3B", ""},
		'\\': {"\uFF3C\u2216\u29F5\u29F9\uFE68", "\u3035"},
		']':  {"\uFF3D", ""},
		'^':  {"\uFF3E\u02C4\u02C6\u1DBA\u2303", "\u0302"},
		'_':  {"\uFF3F\u02CD\u268A", "\u0331\u0320\uFE58"},
		'`':  {"\uFF40\u02CB\u1FEF\u2035", "\u0300"},
		'a':  {"\u0251\u0430", "\u00AA\u1D43\u1D45\u2090\u24D0\uFF41"},
		'b':  {"", "\u1D47\u24D1\uFF42"},
		'c':  {"\u03F2\u0441\u217D", "\u1D9C\u24D2\uFF43"},
		'd':  {"\u0501\u217E", "\u1D48\u2146\u24D3\uFF44"},
		'e':  {"\u0435\u1971", "\u1D49\u2091\u212F\u2147\u24D4\uFF45\u19C9"},
		'f':  {"", "\u1DA0\u24D5\uFF46"},
		'g':  {"\u0261", "\u1D4D\u1DA2\u210A\u24D6\uFF47"},
		'h':  {"\u04BB", "\u02B0\u210E\u24D7\uFF48"},
		'i':  {"\u0456\u2170", "\u1D62\u2071\u2139\u2148\u24D8\uFF49"},
		'j':  {"\u03F3\u0458", "\u02B2\u2149\u24D9\u2C7C\uFF4A"},
		'k':  {"", "\u1D4F\u24DA\uFF4B"},
		'l':  {"\u217C", "\u02E1\u2113\u24DB\uFF4C"},
		'm':  {"\u217F", "\u1D50\u24DC\uFF4D"},
		'n':  {"\u1952", "\u207F\u24DD\uFF4E"},
		'o':  {"\u03BF\u043E\u0D20\u2C9F", "\u00BA\u1D52\u2092\u2134\u24DE\uFF4F\u0CE6\u0D66\u199E\u19D0"},
		'p':  {"\u0440\u2CA3", "\u1D56\u24DF\uFF50"},
		'q':  {"", "\u24E0\uFF51"},
		'r':  {"", "\u02B3\u1D63\u24E1\uFF52"},
		's':  {"\u0455", "\u02E2\u24E2\uFF53"},
		't':  {"", "\u1D57\u24E3\uFF54"},
		'u':  {"\u1959\u222A", "\u1D58\u1D64\u24E4\uFF55"},
		'v':  {"\u1D20\u2174\u2228\u22C1", "\u1D5B\u1D65\u24E5\u2C7D\uFF56"},
		'w':  {"\u1D21", "\u02B7\u24E6\uFF57"},
		'x':  {"\u0445\u2179\u2CAD", "\u02E3\u2093\u24E7\uFF58"},
		'y':  {"\u0443\u1EFF", "\u02B8\u24E8\uFF59"},
		'z':  {"\u1D22", "\u1DBB\u24E9\uFF5A\u1901"},
		'{':  {"\uFF5B\uFE5B", ""},
		'|':  {"\uFF5C\u01C0\u16C1\u239C\u239F\u23A2\u23A5\u23AA\u23AE\uFFE8", "\uFE33\u0846\u1175\u20D2\u2F01\u3021\u4E28\uFE31"},
		'}':  {"\uFF5D\uFE5C", ""},
		'~':  {"\uFF5E\u02DC\u2053\u223C", "\u301C"},
	}

	for ascii, glyphs := range mimicHomoglyphs {
		runes := []rune(glyphs[0]) // ignore loosely-related reverse glyphs
		for _, r := range runes {
			addStringAscii(string(r), ascii)
			addAsciiString(ascii, r)
		}
	}

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("No caller information")
	}

	// https://raw.githubusercontent.com/life4/homoglyphs/master/homoglyphs/confusables.json
	data, err := ioutil.ReadFile(filepath.Join(path.Dir(filename), "homoglyphs.json"))
	if err != nil {
		panic(err)
	}

	var jsonMap map[string][]string
	err = json.Unmarshal(data, &jsonMap)
	if err != nil {
		panic(err)
	}

	for k, v := range jsonMap {
		if len(k) != 1 { // Skip multi-ascii-character homoglyphs
			continue
		}

		ascii := []rune(k)[0]
		if k[0] > unicode.MaxASCII {
			continue
		}

		for _, str := range v {
			if len(str) == 0 {
				panic(fmt.Sprintf("JSON contains empty string for key: %s", k))
			}

			addStringAscii(str, ascii)
			//addAsciiString(ascii, []rune(str)[0])
		}
	}

	// https://raw.githubusercontent.com/codebox/homoglyph/master/raw_data/chars.txt
	file, err := os.Open(filepath.Join(path.Dir(filename), "homoglyphs.txt"))
	if err != nil {
		panic(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\n")
		if line[0] == '#' {
			continue
		}

		asciiIdxs := findDomainCharIndexes(line)

		runes := []rune(line)
		for idx, r := range runes {
			if indexesContain(asciiIdxs, idx) {
				continue
			}

			for _, asciiIdx := range asciiIdxs {
				addStringAscii(string(r), runes[asciiIdx])
				addAsciiString(runes[asciiIdx], r)
			}
		}
	}
}

func replaceRunes(base []rune, indexes []int, subs map[int][]rune) []string {
	if len(indexes) == 0 {
		return []string{string(base)}
	}

	replacements := make([]string, 0)
	for i, idx := range indexes {
		for subIdx, sub := range subs[idx] {
			if subIdx == 0 {
				base[idx] = subs[idx][subIdx]
			} else {
				new_base := make([]rune, len(base))
				copy(new_base, base)
				new_base[idx] = sub
				replacements = append(replacements, replaceRunes(new_base, indexes[i+1:], subs)...)
			}
		}
	}
	replacements = append(replacements, string(base))
	return replacements
}

func GetASCIIHomographs(unicodeDomain string) []string {
	domainRunes := []rune(unicodeDomain)

	idxSubstitutions := make(map[int][]rune)
	indexes := make([]int, 0)
	for idx, r := range domainRunes {
		asciiValues, present := GLYPH_TO_ASCII[string(r)]
		if present {
			idxSubstitutions[idx] = asciiValues
			indexes = append(indexes, idx)
		}
	}

	return replaceRunes(domainRunes, indexes, idxSubstitutions)
}

func homoglyphPermutations(ch chan<- Mutation, unicodeDomain string, wg *sync.WaitGroup, startInd, depth, maxDepth int) {
	if depth >= maxDepth || startInd >= len(unicodeDomain) {
		wg.Done()
		return
	}

	domainRunes := []rune(unicodeDomain)
	for idx, r := range domainRunes {
		if idx < startInd {
			continue
		}
		asciiValues, present := ASCII_TO_GLYPH[r]
		if present {
			for _, homoRune := range asciiValues {
				tempSlice := append(make([]rune, 0), domainRunes[:idx]...)
				tempSlice = append(tempSlice, homoRune)
				newString := string(append(tempSlice, domainRunes[idx+1:]...))
				newPunycode, err := idna.ToASCII(newString)
				if err != nil {
					log.Fatalf("Unable to convert %s to punycode: %s", newString, err.Error())
				}
				ch <- Mutation(newPunycode)
				wg.Add(1)
				go homoglyphPermutations(ch, newString, wg, idx+1, depth+1, maxDepth)
			}
		}
	}
	wg.Done()
}

func GenerateASCIIHomographs(mutations chan<- Mutation, unicodeDomain string, maxHomoglyphSubs int) {
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go homoglyphPermutations(mutations, unicodeDomain, wg, 0, 0, maxHomoglyphSubs)
	wg.Wait()
	close(mutations)
}