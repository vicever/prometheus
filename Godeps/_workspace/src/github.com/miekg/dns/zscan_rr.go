package dns

import (
	"encoding/base64"
	"net"
	"strconv"
	"strings"
)

type parserFunc struct {
	// Func defines the function that parses the tokens and returns the RR
	// or an error. The last string contains any comments in the line as
	// they returned by the lexer as well.
	Func func(h RR_Header, c chan lex, origin string, file string) (RR, *ParseError, string)
	// Signals if the RR ending is of variable length, like TXT or records
	// that have Hexadecimal or Base64 as their last element in the Rdata. Records
	// that have a fixed ending or for instance A, AAAA, SOA and etc.
	Variable bool
}

// Parse the rdata of each rrtype.
// All data from the channel c is either _STRING or _BLANK.
// After the rdata there may come a _BLANK and then a _NEWLINE
// or immediately a _NEWLINE. If this is not the case we flag
// an *ParseError: garbage after rdata.
func setRR(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	parserfunc, ok := typeToparserFunc[h.Rrtype]
	if ok {
		r, e, cm := parserfunc.Func(h, c, o, f)
		if parserfunc.Variable {
			return r, e, cm
		}
		if e != nil {
			return nil, e, ""
		}
		e, cm = slurpRemainder(c, f)
		if e != nil {
			return nil, e, ""
		}
		return r, nil, cm
	}
	// RFC3957 RR (Unknown RR handling)
	return setRFC3597(h, c, o, f)
}

// A remainder of the rdata with embedded spaces, return the parsed string (sans the spaces)
// or an error
func endingToString(c chan lex, errstr, f string) (string, *ParseError, string) {
	s := ""
	l := <-c // _STRING
	for l.value != _NEWLINE && l.value != _EOF {
		switch l.value {
		case _STRING:
			s += l.token
		case _BLANK: // Ok
		default:
			return "", &ParseError{f, errstr, l}, ""
		}
		l = <-c
	}
	return s, nil, l.comment
}

// A remainder of the rdata with embedded spaces, return the parsed string slice (sans the spaces)
// or an error
func endingToTxtSlice(c chan lex, errstr, f string) ([]string, *ParseError, string) {
	// Get the remaining data until we see a NEWLINE
	quote := false
	l := <-c
	var s []string
	switch l.value == _QUOTE {
	case true: // A number of quoted string
		s = make([]string, 0)
		empty := true
		for l.value != _NEWLINE && l.value != _EOF {
			switch l.value {
			case _STRING:
				empty = false
				s = append(s, l.token)
			case _BLANK:
				if quote {
					// _BLANK can only be seen in between txt parts.
					return nil, &ParseError{f, errstr, l}, ""
				}
			case _QUOTE:
				if empty && quote {
					s = append(s, "")
				}
				quote = !quote
				empty = true
			default:
				return nil, &ParseError{f, errstr, l}, ""
			}
			l = <-c
		}
		if quote {
			return nil, &ParseError{f, errstr, l}, ""
		}
	case false: // Unquoted text record
		s = make([]string, 1)
		for l.value != _NEWLINE && l.value != _EOF {
			s[0] += l.token
			l = <-c
		}
	}
	return s, nil, l.comment
}

func setA(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(A)
	rr.Hdr = h

	l := <-c
	if l.length == 0 { // Dynamic updates.
		return rr, nil, ""
	}
	rr.A = net.ParseIP(l.token)
	if rr.A == nil {
		return nil, &ParseError{f, "bad A A", l}, ""
	}
	return rr, nil, ""
}

func setAAAA(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(AAAA)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	rr.AAAA = net.ParseIP(l.token)
	if rr.AAAA == nil {
		return nil, &ParseError{f, "bad AAAA AAAA", l}, ""
	}
	return rr, nil, ""
}

func setNS(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NS)
	rr.Hdr = h

	l := <-c
	rr.Ns = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Ns = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad NS Ns", l}, ""
	}
	if rr.Ns[l.length-1] != '.' {
		rr.Ns = appendOrigin(rr.Ns, o)
	}
	return rr, nil, ""
}

func setPTR(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(PTR)
	rr.Hdr = h

	l := <-c
	rr.Ptr = l.token
	if l.length == 0 { // dynamic update rr.
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Ptr = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad PTR Ptr", l}, ""
	}
	if rr.Ptr[l.length-1] != '.' {
		rr.Ptr = appendOrigin(rr.Ptr, o)
	}
	return rr, nil, ""
}

func setNSAPPTR(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NSAPPTR)
	rr.Hdr = h

	l := <-c
	rr.Ptr = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Ptr = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad NSAP-PTR Ptr", l}, ""
	}
	if rr.Ptr[l.length-1] != '.' {
		rr.Ptr = appendOrigin(rr.Ptr, o)
	}
	return rr, nil, ""
}

func setRP(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(RP)
	rr.Hdr = h

	l := <-c
	rr.Mbox = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Mbox = o
	} else {
		_, ok := IsDomainName(l.token)
		if !ok {
			return nil, &ParseError{f, "bad RP Mbox", l}, ""
		}
		if rr.Mbox[l.length-1] != '.' {
			rr.Mbox = appendOrigin(rr.Mbox, o)
		}
	}
	<-c // _BLANK
	l = <-c
	rr.Txt = l.token
	if l.token == "@" {
		rr.Txt = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad RP Txt", l}, ""
	}
	if rr.Txt[l.length-1] != '.' {
		rr.Txt = appendOrigin(rr.Txt, o)
	}
	return rr, nil, ""
}

func setMR(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(MR)
	rr.Hdr = h

	l := <-c
	rr.Mr = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Mr = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad MR Mr", l}, ""
	}
	if rr.Mr[l.length-1] != '.' {
		rr.Mr = appendOrigin(rr.Mr, o)
	}
	return rr, nil, ""
}

func setMB(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(MB)
	rr.Hdr = h

	l := <-c
	rr.Mb = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Mb = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad MB Mb", l}, ""
	}
	if rr.Mb[l.length-1] != '.' {
		rr.Mb = appendOrigin(rr.Mb, o)
	}
	return rr, nil, ""
}

func setMG(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(MG)
	rr.Hdr = h

	l := <-c
	rr.Mg = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Mg = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad MG Mg", l}, ""
	}
	if rr.Mg[l.length-1] != '.' {
		rr.Mg = appendOrigin(rr.Mg, o)
	}
	return rr, nil, ""
}

func setHINFO(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(HINFO)
	rr.Hdr = h

	l := <-c
	rr.Cpu = l.token
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Os = l.token

	return rr, nil, ""
}

func setMINFO(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(MINFO)
	rr.Hdr = h

	l := <-c
	rr.Rmail = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Rmail = o
	} else {
		_, ok := IsDomainName(l.token)
		if !ok {
			return nil, &ParseError{f, "bad MINFO Rmail", l}, ""
		}
		if rr.Rmail[l.length-1] != '.' {
			rr.Rmail = appendOrigin(rr.Rmail, o)
		}
	}
	<-c // _BLANK
	l = <-c
	rr.Email = l.token
	if l.token == "@" {
		rr.Email = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad MINFO Email", l}, ""
	}
	if rr.Email[l.length-1] != '.' {
		rr.Email = appendOrigin(rr.Email, o)
	}
	return rr, nil, ""
}

func setMF(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(MF)
	rr.Hdr = h

	l := <-c
	rr.Mf = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Mf = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad MF Mf", l}, ""
	}
	if rr.Mf[l.length-1] != '.' {
		rr.Mf = appendOrigin(rr.Mf, o)
	}
	return rr, nil, ""
}

func setMD(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(MD)
	rr.Hdr = h

	l := <-c
	rr.Md = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Md = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad MD Md", l}, ""
	}
	if rr.Md[l.length-1] != '.' {
		rr.Md = appendOrigin(rr.Md, o)
	}
	return rr, nil, ""
}

func setMX(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(MX)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad MX Pref", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Mx = l.token
	if l.token == "@" {
		rr.Mx = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad MX Mx", l}, ""
	}
	if rr.Mx[l.length-1] != '.' {
		rr.Mx = appendOrigin(rr.Mx, o)
	}
	return rr, nil, ""
}

func setRT(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(RT)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad RT Preference", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Host = l.token
	if l.token == "@" {
		rr.Host = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad RT Host", l}, ""
	}
	if rr.Host[l.length-1] != '.' {
		rr.Host = appendOrigin(rr.Host, o)
	}
	return rr, nil, ""
}

func setAFSDB(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(AFSDB)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad AFSDB Subtype", l}, ""
	} else {
		rr.Subtype = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Hostname = l.token
	if l.token == "@" {
		rr.Hostname = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad AFSDB Hostname", l}, ""
	}
	if rr.Hostname[l.length-1] != '.' {
		rr.Hostname = appendOrigin(rr.Hostname, o)
	}
	return rr, nil, ""
}

func setX25(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(X25)
	rr.Hdr = h

	l := <-c
	rr.PSDNAddress = l.token
	return rr, nil, ""
}

func setKX(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(KX)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad KX Pref", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Exchanger = l.token
	if l.token == "@" {
		rr.Exchanger = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad KX Exchanger", l}, ""
	}
	if rr.Exchanger[l.length-1] != '.' {
		rr.Exchanger = appendOrigin(rr.Exchanger, o)
	}
	return rr, nil, ""
}

func setCNAME(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(CNAME)
	rr.Hdr = h

	l := <-c
	rr.Target = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Target = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad CNAME Target", l}, ""
	}
	if rr.Target[l.length-1] != '.' {
		rr.Target = appendOrigin(rr.Target, o)
	}
	return rr, nil, ""
}

func setDNAME(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(DNAME)
	rr.Hdr = h

	l := <-c
	rr.Target = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Target = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad CNAME Target", l}, ""
	}
	if rr.Target[l.length-1] != '.' {
		rr.Target = appendOrigin(rr.Target, o)
	}
	return rr, nil, ""
}

func setSOA(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(SOA)
	rr.Hdr = h

	l := <-c
	rr.Ns = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	<-c // _BLANK
	if l.token == "@" {
		rr.Ns = o
	} else {
		_, ok := IsDomainName(l.token)
		if !ok {
			return nil, &ParseError{f, "bad SOA Ns", l}, ""
		}
		if rr.Ns[l.length-1] != '.' {
			rr.Ns = appendOrigin(rr.Ns, o)
		}
	}

	l = <-c
	rr.Mbox = l.token
	if l.token == "@" {
		rr.Mbox = o
	} else {
		_, ok := IsDomainName(l.token)
		if !ok || l.length == 0 {
			return nil, &ParseError{f, "bad SOA Mbox", l}, ""
		}
		if rr.Mbox[l.length-1] != '.' {
			rr.Mbox = appendOrigin(rr.Mbox, o)
		}
	}
	<-c // _BLANK

	var (
		v  uint32
		ok bool
	)
	for i := 0; i < 5; i++ {
		l = <-c
		if j, e := strconv.Atoi(l.token); e != nil {
			if i == 0 {
				// Serial should be a number
				return nil, &ParseError{f, "bad SOA zone parameter", l}, ""
			}
			if v, ok = stringToTtl(l.token); !ok {
				return nil, &ParseError{f, "bad SOA zone parameter", l}, ""

			}
		} else {
			v = uint32(j)
		}
		switch i {
		case 0:
			rr.Serial = v
			<-c // _BLANK
		case 1:
			rr.Refresh = v
			<-c // _BLANK
		case 2:
			rr.Retry = v
			<-c // _BLANK
		case 3:
			rr.Expire = v
			<-c // _BLANK
		case 4:
			rr.Minttl = v
		}
	}
	return rr, nil, ""
}

func setSRV(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(SRV)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad SRV Priority", l}, ""
	} else {
		rr.Priority = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad SRV Weight", l}, ""
	} else {
		rr.Weight = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad SRV Port", l}, ""
	} else {
		rr.Port = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Target = l.token
	if l.token == "@" {
		rr.Target = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad SRV Target", l}, ""
	}
	if rr.Target[l.length-1] != '.' {
		rr.Target = appendOrigin(rr.Target, o)
	}
	return rr, nil, ""
}

func setNAPTR(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NAPTR)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NAPTR Order", l}, ""
	} else {
		rr.Order = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NAPTR Preference", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	// Flags
	<-c     // _BLANK
	l = <-c // _QUOTE
	if l.value != _QUOTE {
		return nil, &ParseError{f, "bad NAPTR Flags", l}, ""
	}
	l = <-c // Either String or Quote
	if l.value == _STRING {
		rr.Flags = l.token
		l = <-c // _QUOTE
		if l.value != _QUOTE {
			return nil, &ParseError{f, "bad NAPTR Flags", l}, ""
		}
	} else if l.value == _QUOTE {
		rr.Flags = ""
	} else {
		return nil, &ParseError{f, "bad NAPTR Flags", l}, ""
	}

	// Service
	<-c     // _BLANK
	l = <-c // _QUOTE
	if l.value != _QUOTE {
		return nil, &ParseError{f, "bad NAPTR Service", l}, ""
	}
	l = <-c // Either String or Quote
	if l.value == _STRING {
		rr.Service = l.token
		l = <-c // _QUOTE
		if l.value != _QUOTE {
			return nil, &ParseError{f, "bad NAPTR Service", l}, ""
		}
	} else if l.value == _QUOTE {
		rr.Service = ""
	} else {
		return nil, &ParseError{f, "bad NAPTR Service", l}, ""
	}

	// Regexp
	<-c     // _BLANK
	l = <-c // _QUOTE
	if l.value != _QUOTE {
		return nil, &ParseError{f, "bad NAPTR Regexp", l}, ""
	}
	l = <-c // Either String or Quote
	if l.value == _STRING {
		rr.Regexp = l.token
		l = <-c // _QUOTE
		if l.value != _QUOTE {
			return nil, &ParseError{f, "bad NAPTR Regexp", l}, ""
		}
	} else if l.value == _QUOTE {
		rr.Regexp = ""
	} else {
		return nil, &ParseError{f, "bad NAPTR Regexp", l}, ""
	}
	// After quote no space??
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Replacement = l.token
	if l.token == "@" {
		rr.Replacement = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad NAPTR Replacement", l}, ""
	}
	if rr.Replacement[l.length-1] != '.' {
		rr.Replacement = appendOrigin(rr.Replacement, o)
	}
	return rr, nil, ""
}

func setTALINK(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(TALINK)
	rr.Hdr = h

	l := <-c
	rr.PreviousName = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.PreviousName = o
	} else {
		_, ok := IsDomainName(l.token)
		if !ok {
			return nil, &ParseError{f, "bad TALINK PreviousName", l}, ""
		}
		if rr.PreviousName[l.length-1] != '.' {
			rr.PreviousName = appendOrigin(rr.PreviousName, o)
		}
	}
	<-c // _BLANK
	l = <-c
	rr.NextName = l.token
	if l.token == "@" {
		rr.NextName = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad TALINK NextName", l}, ""
	}
	if rr.NextName[l.length-1] != '.' {
		rr.NextName = appendOrigin(rr.NextName, o)
	}
	return rr, nil, ""
}

func setLOC(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(LOC)
	rr.Hdr = h
	// Non zero defaults for LOC record, see RFC 1876, Section 3.
	rr.HorizPre = 165 // 10000
	rr.VertPre = 162  // 10
	rr.Size = 18      // 1
	ok := false
	// North
	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad LOC Latitude", l}, ""
	} else {
		rr.Latitude = 1000 * 60 * 60 * uint32(i)
	}
	<-c // _BLANK
	// Either number, 'N' or 'S'
	l = <-c
	if rr.Latitude, ok = locCheckNorth(l.token, rr.Latitude); ok {
		goto East
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad LOC Latitude minutes", l}, ""
	} else {
		rr.Latitude += 1000 * 60 * uint32(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.ParseFloat(l.token, 32); e != nil {
		return nil, &ParseError{f, "bad LOC Latitude seconds", l}, ""
	} else {
		rr.Latitude += uint32(1000 * i)
	}
	<-c // _BLANK
	// Either number, 'N' or 'S'
	l = <-c
	if rr.Latitude, ok = locCheckNorth(l.token, rr.Latitude); ok {
		goto East
	}
	// If still alive, flag an error
	return nil, &ParseError{f, "bad LOC Latitude North/South", l}, ""

East:
	// East
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad LOC Longitude", l}, ""
	} else {
		rr.Longitude = 1000 * 60 * 60 * uint32(i)
	}
	<-c // _BLANK
	// Either number, 'E' or 'W'
	l = <-c
	if rr.Longitude, ok = locCheckEast(l.token, rr.Longitude); ok {
		goto Altitude
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad LOC Longitude minutes", l}, ""
	} else {
		rr.Longitude += 1000 * 60 * uint32(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.ParseFloat(l.token, 32); e != nil {
		return nil, &ParseError{f, "bad LOC Longitude seconds", l}, ""
	} else {
		rr.Longitude += uint32(1000 * i)
	}
	<-c // _BLANK
	// Either number, 'E' or 'W'
	l = <-c
	if rr.Longitude, ok = locCheckEast(l.token, rr.Longitude); ok {
		goto Altitude
	}
	// If still alive, flag an error
	return nil, &ParseError{f, "bad LOC Longitude East/West", l}, ""

Altitude:
	<-c // _BLANK
	l = <-c
	if l.token[len(l.token)-1] == 'M' || l.token[len(l.token)-1] == 'm' {
		l.token = l.token[0 : len(l.token)-1]
	}
	if i, e := strconv.ParseFloat(l.token, 32); e != nil {
		return nil, &ParseError{f, "bad LOC Altitude", l}, ""
	} else {
		rr.Altitude = uint32(i*100.0 + 10000000.0 + 0.5)
	}

	// And now optionally the other values
	l = <-c
	count := 0
	for l.value != _NEWLINE && l.value != _EOF {
		switch l.value {
		case _STRING:
			switch count {
			case 0: // Size
				if e, m, ok := stringToCm(l.token); !ok {
					return nil, &ParseError{f, "bad LOC Size", l}, ""
				} else {
					rr.Size = (e & 0x0f) | (m << 4 & 0xf0)
				}
			case 1: // HorizPre
				if e, m, ok := stringToCm(l.token); !ok {
					return nil, &ParseError{f, "bad LOC HorizPre", l}, ""
				} else {
					rr.HorizPre = (e & 0x0f) | (m << 4 & 0xf0)
				}
			case 2: // VertPre
				if e, m, ok := stringToCm(l.token); !ok {
					return nil, &ParseError{f, "bad LOC VertPre", l}, ""
				} else {
					rr.VertPre = (e & 0x0f) | (m << 4 & 0xf0)
				}
			}
			count++
		case _BLANK:
			// Ok
		default:
			return nil, &ParseError{f, "bad LOC Size, HorizPre or VertPre", l}, ""
		}
		l = <-c
	}
	return rr, nil, ""
}

func setHIP(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(HIP)
	rr.Hdr = h

	// HitLength is not represented
	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad HIP PublicKeyAlgorithm", l}, ""
	} else {
		rr.PublicKeyAlgorithm = uint8(i)
	}
	<-c              // _BLANK
	l = <-c          // _STRING
	rr.Hit = l.token // This can not contain spaces, see RFC 5205 Section 6.
	rr.HitLength = uint8(len(rr.Hit)) / 2

	<-c                    // _BLANK
	l = <-c                // _STRING
	rr.PublicKey = l.token // This cannot contain spaces
	rr.PublicKeyLength = uint16(base64.StdEncoding.DecodedLen(len(rr.PublicKey)))

	// RendezvousServers (if any)
	l = <-c
	xs := make([]string, 0)
	for l.value != _NEWLINE && l.value != _EOF {
		switch l.value {
		case _STRING:
			if l.token == "@" {
				xs = append(xs, o)
				continue
			}
			_, ok := IsDomainName(l.token)
			if !ok || l.length == 0 {
				return nil, &ParseError{f, "bad HIP RendezvousServers", l}, ""
			}
			if l.token[l.length-1] != '.' {
				l.token = appendOrigin(l.token, o)
			}
			xs = append(xs, l.token)
		case _BLANK:
			// Ok
		default:
			return nil, &ParseError{f, "bad HIP RendezvousServers", l}, ""
		}
		l = <-c
	}
	rr.RendezvousServers = xs
	return rr, nil, l.comment
}

func setCERT(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(CERT)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if v, ok := StringToCertType[l.token]; ok {
		rr.Type = v
	} else if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad CERT Type", l}, ""
	} else {
		rr.Type = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad CERT KeyTag", l}, ""
	} else {
		rr.KeyTag = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if v, ok := StringToAlgorithm[l.token]; ok {
		rr.Algorithm = v
	} else if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad CERT Algorithm", l}, ""
	} else {
		rr.Algorithm = uint8(i)
	}
	s, e, c1 := endingToString(c, "bad CERT Certificate", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Certificate = s
	return rr, nil, c1
}

func setOPENPGPKEY(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(OPENPGPKEY)
	rr.Hdr = h

	s, e, c1 := endingToString(c, "bad OPENPGPKEY PublicKey", f)
	if e != nil {
		return nil, e, c1
	}
	rr.PublicKey = s
	return rr, nil, c1
}

func setRRSIG(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(RRSIG)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if t, ok := StringToType[l.tokenUpper]; !ok {
		if strings.HasPrefix(l.tokenUpper, "TYPE") {
			if t, ok = typeToInt(l.tokenUpper); !ok {
				return nil, &ParseError{f, "bad RRSIG Typecovered", l}, ""
			} else {
				rr.TypeCovered = t
			}
		} else {
			return nil, &ParseError{f, "bad RRSIG Typecovered", l}, ""
		}
	} else {
		rr.TypeCovered = t
	}
	<-c // _BLANK
	l = <-c
	if i, err := strconv.Atoi(l.token); err != nil {
		return nil, &ParseError{f, "bad RRSIG Algorithm", l}, ""
	} else {
		rr.Algorithm = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, err := strconv.Atoi(l.token); err != nil {
		return nil, &ParseError{f, "bad RRSIG Labels", l}, ""
	} else {
		rr.Labels = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, err := strconv.Atoi(l.token); err != nil {
		return nil, &ParseError{f, "bad RRSIG OrigTtl", l}, ""
	} else {
		rr.OrigTtl = uint32(i)
	}
	<-c // _BLANK
	l = <-c
	if i, err := StringToTime(l.token); err != nil {
		// Try to see if all numeric and use it as epoch
		if i, err := strconv.ParseInt(l.token, 10, 64); err == nil {
			// TODO(miek): error out on > MAX_UINT32, same below
			rr.Expiration = uint32(i)
		} else {
			return nil, &ParseError{f, "bad RRSIG Expiration", l}, ""
		}
	} else {
		rr.Expiration = i
	}
	<-c // _BLANK
	l = <-c
	if i, err := StringToTime(l.token); err != nil {
		if i, err := strconv.ParseInt(l.token, 10, 64); err == nil {
			rr.Inception = uint32(i)
		} else {
			return nil, &ParseError{f, "bad RRSIG Inception", l}, ""
		}
	} else {
		rr.Inception = i
	}
	<-c // _BLANK
	l = <-c
	if i, err := strconv.Atoi(l.token); err != nil {
		return nil, &ParseError{f, "bad RRSIG KeyTag", l}, ""
	} else {
		rr.KeyTag = uint16(i)
	}
	<-c // _BLANK
	l = <-c
	rr.SignerName = l.token
	if l.token == "@" {
		rr.SignerName = o
	} else {
		_, ok := IsDomainName(l.token)
		if !ok || l.length == 0 {
			return nil, &ParseError{f, "bad RRSIG SignerName", l}, ""
		}
		if rr.SignerName[l.length-1] != '.' {
			rr.SignerName = appendOrigin(rr.SignerName, o)
		}
	}
	s, e, c1 := endingToString(c, "bad RRSIG Signature", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Signature = s
	return rr, nil, c1
}

func setNSEC(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NSEC)
	rr.Hdr = h

	l := <-c
	rr.NextDomain = l.token
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if l.token == "@" {
		rr.NextDomain = o
	} else {
		_, ok := IsDomainName(l.token)
		if !ok {
			return nil, &ParseError{f, "bad NSEC NextDomain", l}, ""
		}
		if rr.NextDomain[l.length-1] != '.' {
			rr.NextDomain = appendOrigin(rr.NextDomain, o)
		}
	}

	rr.TypeBitMap = make([]uint16, 0)
	var (
		k  uint16
		ok bool
	)
	l = <-c
	for l.value != _NEWLINE && l.value != _EOF {
		switch l.value {
		case _BLANK:
			// Ok
		case _STRING:
			if k, ok = StringToType[l.tokenUpper]; !ok {
				if k, ok = typeToInt(l.tokenUpper); !ok {
					return nil, &ParseError{f, "bad NSEC TypeBitMap", l}, ""
				}
			}
			rr.TypeBitMap = append(rr.TypeBitMap, k)
		default:
			return nil, &ParseError{f, "bad NSEC TypeBitMap", l}, ""
		}
		l = <-c
	}
	return rr, nil, l.comment
}

func setNSEC3(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NSEC3)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NSEC3 Hash", l}, ""
	} else {
		rr.Hash = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NSEC3 Flags", l}, ""
	} else {
		rr.Flags = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NSEC3 Iterations", l}, ""
	} else {
		rr.Iterations = uint16(i)
	}
	<-c
	l = <-c
	if len(l.token) == 0 {
		return nil, &ParseError{f, "bad NSEC3 Salt", l}, ""
	}
	rr.SaltLength = uint8(len(l.token)) / 2
	rr.Salt = l.token

	<-c
	l = <-c
	rr.HashLength = 20 // Fix for NSEC3 (sha1 160 bits)
	rr.NextDomain = l.token

	rr.TypeBitMap = make([]uint16, 0)
	var (
		k  uint16
		ok bool
	)
	l = <-c
	for l.value != _NEWLINE && l.value != _EOF {
		switch l.value {
		case _BLANK:
			// Ok
		case _STRING:
			if k, ok = StringToType[l.tokenUpper]; !ok {
				if k, ok = typeToInt(l.tokenUpper); !ok {
					return nil, &ParseError{f, "bad NSEC3 TypeBitMap", l}, ""
				}
			}
			rr.TypeBitMap = append(rr.TypeBitMap, k)
		default:
			return nil, &ParseError{f, "bad NSEC3 TypeBitMap", l}, ""
		}
		l = <-c
	}
	return rr, nil, l.comment
}

func setNSEC3PARAM(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NSEC3PARAM)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NSEC3PARAM Hash", l}, ""
	} else {
		rr.Hash = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NSEC3PARAM Flags", l}, ""
	} else {
		rr.Flags = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NSEC3PARAM Iterations", l}, ""
	} else {
		rr.Iterations = uint16(i)
	}
	<-c
	l = <-c
	rr.SaltLength = uint8(len(l.token))
	rr.Salt = l.token
	return rr, nil, ""
}

func setEUI48(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(EUI48)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.length != 17 {
		return nil, &ParseError{f, "bad EUI48 Address", l}, ""
	}
	addr := make([]byte, 12)
	dash := 0
	for i := 0; i < 10; i += 2 {
		addr[i] = l.token[i+dash]
		addr[i+1] = l.token[i+1+dash]
		dash++
		if l.token[i+1+dash] != '-' {
			return nil, &ParseError{f, "bad EUI48 Address", l}, ""
		}
	}
	addr[10] = l.token[15]
	addr[11] = l.token[16]

	if i, e := strconv.ParseUint(string(addr), 16, 48); e != nil {
		return nil, &ParseError{f, "bad EUI48 Address", l}, ""
	} else {
		rr.Address = i
	}
	return rr, nil, ""
}

func setEUI64(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(EUI64)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.length != 23 {
		return nil, &ParseError{f, "bad EUI64 Address", l}, ""
	}
	addr := make([]byte, 16)
	dash := 0
	for i := 0; i < 14; i += 2 {
		addr[i] = l.token[i+dash]
		addr[i+1] = l.token[i+1+dash]
		dash++
		if l.token[i+1+dash] != '-' {
			return nil, &ParseError{f, "bad EUI64 Address", l}, ""
		}
	}
	addr[14] = l.token[21]
	addr[15] = l.token[22]

	if i, e := strconv.ParseUint(string(addr), 16, 64); e != nil {
		return nil, &ParseError{f, "bad EUI68 Address", l}, ""
	} else {
		rr.Address = uint64(i)
	}
	return rr, nil, ""
}

func setWKS(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(WKS)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	rr.Address = net.ParseIP(l.token)
	if rr.Address == nil {
		return nil, &ParseError{f, "bad WKS Address", l}, ""
	}

	<-c // _BLANK
	l = <-c
	proto := "tcp"
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad WKS Protocol", l}, ""
	} else {
		rr.Protocol = uint8(i)
		switch rr.Protocol {
		case 17:
			proto = "udp"
		case 6:
			proto = "tcp"
		default:
			return nil, &ParseError{f, "bad WKS Protocol", l}, ""
		}
	}

	<-c
	l = <-c
	rr.BitMap = make([]uint16, 0)
	var (
		k   int
		err error
	)
	for l.value != _NEWLINE && l.value != _EOF {
		switch l.value {
		case _BLANK:
			// Ok
		case _STRING:
			if k, err = net.LookupPort(proto, l.token); err != nil {
				if i, e := strconv.Atoi(l.token); e != nil { // If a number use that
					rr.BitMap = append(rr.BitMap, uint16(i))
				} else {
					return nil, &ParseError{f, "bad WKS BitMap", l}, ""
				}
			}
			rr.BitMap = append(rr.BitMap, uint16(k))
		default:
			return nil, &ParseError{f, "bad WKS BitMap", l}, ""
		}
		l = <-c
	}
	return rr, nil, l.comment
}

func setSSHFP(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(SSHFP)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad SSHFP Algorithm", l}, ""
	} else {
		rr.Algorithm = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad SSHFP Type", l}, ""
	} else {
		rr.Type = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	rr.FingerPrint = l.token
	return rr, nil, ""
}

func setDNSKEY(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(DNSKEY)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad DNSKEY Flags", l}, ""
	} else {
		rr.Flags = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad DNSKEY Protocol", l}, ""
	} else {
		rr.Protocol = uint8(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad DNSKEY Algorithm", l}, ""
	} else {
		rr.Algorithm = uint8(i)
	}
	s, e, c1 := endingToString(c, "bad DNSKEY PublicKey", f)
	if e != nil {
		return nil, e, c1
	}
	rr.PublicKey = s
	return rr, nil, c1
}

func setRKEY(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(RKEY)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad RKEY Flags", l}, ""
	} else {
		rr.Flags = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad RKEY Protocol", l}, ""
	} else {
		rr.Protocol = uint8(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad RKEY Algorithm", l}, ""
	} else {
		rr.Algorithm = uint8(i)
	}
	s, e, c1 := endingToString(c, "bad RKEY PublicKey", f)
	if e != nil {
		return nil, e, c1
	}
	rr.PublicKey = s
	return rr, nil, c1
}

func setDS(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(DS)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad DS KeyTag", l}, ""
	} else {
		rr.KeyTag = uint16(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		if i, ok := StringToAlgorithm[l.tokenUpper]; !ok {
			return nil, &ParseError{f, "bad DS Algorithm", l}, ""
		} else {
			rr.Algorithm = i
		}
	} else {
		rr.Algorithm = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad DS DigestType", l}, ""
	} else {
		rr.DigestType = uint8(i)
	}
	s, e, c1 := endingToString(c, "bad DS Digest", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Digest = s
	return rr, nil, c1
}

func setEID(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(EID)
	rr.Hdr = h
	s, e, c1 := endingToString(c, "bad EID Endpoint", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Endpoint = s
	return rr, nil, c1
}

func setNIMLOC(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NIMLOC)
	rr.Hdr = h
	s, e, c1 := endingToString(c, "bad NIMLOC Locator", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Locator = s
	return rr, nil, c1
}

func setNSAP(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NSAP)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NSAP Length", l}, ""
	} else {
		rr.Length = uint8(i)
	}
	<-c // _BLANK
	s, e, c1 := endingToString(c, "bad NSAP Nsap", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Nsap = s
	return rr, nil, c1
}

func setGPOS(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(GPOS)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if _, e := strconv.ParseFloat(l.token, 64); e != nil {
		return nil, &ParseError{f, "bad GPOS Longitude", l}, ""
	} else {
		rr.Longitude = l.token
	}
	<-c // _BLANK
	l = <-c
	if _, e := strconv.ParseFloat(l.token, 64); e != nil {
		return nil, &ParseError{f, "bad GPOS Latitude", l}, ""
	} else {
		rr.Latitude = l.token
	}
	<-c // _BLANK
	l = <-c
	if _, e := strconv.ParseFloat(l.token, 64); e != nil {
		return nil, &ParseError{f, "bad GPOS Altitude", l}, ""
	} else {
		rr.Altitude = l.token
	}
	return rr, nil, ""
}

func setCDS(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(CDS)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad CDS KeyTag", l}, ""
	} else {
		rr.KeyTag = uint16(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		if i, ok := StringToAlgorithm[l.tokenUpper]; !ok {
			return nil, &ParseError{f, "bad CDS Algorithm", l}, ""
		} else {
			rr.Algorithm = i
		}
	} else {
		rr.Algorithm = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad CDS DigestType", l}, ""
	} else {
		rr.DigestType = uint8(i)
	}
	s, e, c1 := endingToString(c, "bad CDS Digest", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Digest = s
	return rr, nil, c1
}

func setDLV(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(DLV)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad DLV KeyTag", l}, ""
	} else {
		rr.KeyTag = uint16(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		if i, ok := StringToAlgorithm[l.tokenUpper]; !ok {
			return nil, &ParseError{f, "bad DLV Algorithm", l}, ""
		} else {
			rr.Algorithm = i
		}
	} else {
		rr.Algorithm = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad DLV DigestType", l}, ""
	} else {
		rr.DigestType = uint8(i)
	}
	s, e, c1 := endingToString(c, "bad DLV Digest", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Digest = s
	return rr, nil, c1
}

func setTA(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(TA)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad TA KeyTag", l}, ""
	} else {
		rr.KeyTag = uint16(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		if i, ok := StringToAlgorithm[l.tokenUpper]; !ok {
			return nil, &ParseError{f, "bad TA Algorithm", l}, ""
		} else {
			rr.Algorithm = i
		}
	} else {
		rr.Algorithm = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad TA DigestType", l}, ""
	} else {
		rr.DigestType = uint8(i)
	}
	s, e, c1 := endingToString(c, "bad TA Digest", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Digest = s
	return rr, nil, c1
}

func setTLSA(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(TLSA)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad TLSA Usage", l}, ""
	} else {
		rr.Usage = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad TLSA Selector", l}, ""
	} else {
		rr.Selector = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad TLSA MatchingType", l}, ""
	} else {
		rr.MatchingType = uint8(i)
	}
	s, e, c1 := endingToString(c, "bad TLSA Certificate", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Certificate = s
	return rr, nil, c1
}

func setRFC3597(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(RFC3597)
	rr.Hdr = h
	l := <-c
	if l.token != "\\#" {
		return nil, &ParseError{f, "bad RFC3597 Rdata", l}, ""
	}
	<-c // _BLANK
	l = <-c
	rdlength, e := strconv.Atoi(l.token)
	if e != nil {
		return nil, &ParseError{f, "bad RFC3597 Rdata ", l}, ""
	}

	s, e1, c1 := endingToString(c, "bad RFC3597 Rdata", f)
	if e1 != nil {
		return nil, e1, c1
	}
	if rdlength*2 != len(s) {
		return nil, &ParseError{f, "bad RFC3597 Rdata", l}, ""
	}
	rr.Rdata = s
	return rr, nil, c1
}

func setSPF(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(SPF)
	rr.Hdr = h

	s, e, c1 := endingToTxtSlice(c, "bad SPF Txt", f)
	if e != nil {
		return nil, e, ""
	}
	rr.Txt = s
	return rr, nil, c1
}

func setTXT(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(TXT)
	rr.Hdr = h

	// No _BLANK reading here, because this is all rdata is TXT
	s, e, c1 := endingToTxtSlice(c, "bad TXT Txt", f)
	if e != nil {
		return nil, e, ""
	}
	rr.Txt = s
	return rr, nil, c1
}

// identical to setTXT
func setNINFO(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NINFO)
	rr.Hdr = h

	s, e, c1 := endingToTxtSlice(c, "bad NINFO ZSData", f)
	if e != nil {
		return nil, e, ""
	}
	rr.ZSData = s
	return rr, nil, c1
}

func setURI(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(URI)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad URI Priority", l}, ""
	} else {
		rr.Priority = uint16(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad URI Weight", l}, ""
	} else {
		rr.Weight = uint16(i)
	}

	<-c // _BLANK
	s, e, c1 := endingToTxtSlice(c, "bad URI Target", f)
	if e != nil {
		return nil, e, ""
	}
	rr.Target = s
	return rr, nil, c1
}

func setIPSECKEY(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(IPSECKEY)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, l.comment
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad IPSECKEY Precedence", l}, ""
	} else {
		rr.Precedence = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad IPSECKEY GatewayType", l}, ""
	} else {
		rr.GatewayType = uint8(i)
	}
	<-c // _BLANK
	l = <-c
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad IPSECKEY Algorithm", l}, ""
	} else {
		rr.Algorithm = uint8(i)
	}
	<-c
	l = <-c
	rr.Gateway = l.token
	s, e, c1 := endingToString(c, "bad IPSECKEY PublicKey", f)
	if e != nil {
		return nil, e, c1
	}
	rr.PublicKey = s
	return rr, nil, c1
}

func setDHCID(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	// awesome record to parse!
	rr := new(DHCID)
	rr.Hdr = h

	s, e, c1 := endingToString(c, "bad DHCID Digest", f)
	if e != nil {
		return nil, e, c1
	}
	rr.Digest = s
	return rr, nil, c1
}

func setNID(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(NID)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad NID Preference", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	u, err := stringToNodeID(l)
	if err != nil {
		return nil, err, ""
	}
	rr.NodeID = u
	return rr, nil, ""
}

func setL32(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(L32)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad L32 Preference", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Locator32 = net.ParseIP(l.token)
	if rr.Locator32 == nil {
		return nil, &ParseError{f, "bad L32 Locator", l}, ""
	}
	return rr, nil, ""
}

func setLP(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(LP)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad LP Preference", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Fqdn = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Fqdn = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad LP Fqdn", l}, ""
	}
	if rr.Fqdn[l.length-1] != '.' {
		rr.Fqdn = appendOrigin(rr.Fqdn, o)
	}
	return rr, nil, ""
}

func setL64(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(L64)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad L64 Preference", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	u, err := stringToNodeID(l)
	if err != nil {
		return nil, err, ""
	}
	rr.Locator64 = u
	return rr, nil, ""
}

func setUID(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(UID)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad UID Uid", l}, ""
	} else {
		rr.Uid = uint32(i)
	}
	return rr, nil, ""
}

func setGID(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(GID)
	rr.Hdr = h
	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad GID Gid", l}, ""
	} else {
		rr.Gid = uint32(i)
	}
	return rr, nil, ""
}

func setUINFO(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(UINFO)
	rr.Hdr = h
	s, e, c1 := endingToTxtSlice(c, "bad UINFO Uinfo", f)
	if e != nil {
		return nil, e, ""
	}
	rr.Uinfo = s[0] // silently discard anything above
	return rr, nil, c1
}

func setPX(h RR_Header, c chan lex, o, f string) (RR, *ParseError, string) {
	rr := new(PX)
	rr.Hdr = h

	l := <-c
	if l.length == 0 {
		return rr, nil, ""
	}
	if i, e := strconv.Atoi(l.token); e != nil {
		return nil, &ParseError{f, "bad PX Preference", l}, ""
	} else {
		rr.Preference = uint16(i)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Map822 = l.token
	if l.length == 0 {
		return rr, nil, ""
	}
	if l.token == "@" {
		rr.Map822 = o
		return rr, nil, ""
	}
	_, ok := IsDomainName(l.token)
	if !ok {
		return nil, &ParseError{f, "bad PX Map822", l}, ""
	}
	if rr.Map822[l.length-1] != '.' {
		rr.Map822 = appendOrigin(rr.Map822, o)
	}
	<-c     // _BLANK
	l = <-c // _STRING
	rr.Mapx400 = l.token
	if l.token == "@" {
		rr.Mapx400 = o
		return rr, nil, ""
	}
	_, ok = IsDomainName(l.token)
	if !ok || l.length == 0 {
		return nil, &ParseError{f, "bad PX Mapx400", l}, ""
	}
	if rr.Mapx400[l.length-1] != '.' {
		rr.Mapx400 = appendOrigin(rr.Mapx400, o)
	}
	return rr, nil, ""
}

var typeToparserFunc = map[uint16]parserFunc{
	TypeAAAA:       parserFunc{setAAAA, false},
	TypeAFSDB:      parserFunc{setAFSDB, false},
	TypeA:          parserFunc{setA, false},
	TypeCDS:        parserFunc{setCDS, true},
	TypeCERT:       parserFunc{setCERT, true},
	TypeCNAME:      parserFunc{setCNAME, false},
	TypeDHCID:      parserFunc{setDHCID, true},
	TypeDLV:        parserFunc{setDLV, true},
	TypeDNAME:      parserFunc{setDNAME, false},
	TypeDNSKEY:     parserFunc{setDNSKEY, true},
	TypeDS:         parserFunc{setDS, true},
	TypeEID:        parserFunc{setEID, true},
	TypeEUI48:      parserFunc{setEUI48, false},
	TypeEUI64:      parserFunc{setEUI64, false},
	TypeGID:        parserFunc{setGID, false},
	TypeGPOS:       parserFunc{setGPOS, false},
	TypeHINFO:      parserFunc{setHINFO, false},
	TypeHIP:        parserFunc{setHIP, true},
	TypeIPSECKEY:   parserFunc{setIPSECKEY, true},
	TypeKX:         parserFunc{setKX, false},
	TypeL32:        parserFunc{setL32, false},
	TypeL64:        parserFunc{setL64, false},
	TypeLOC:        parserFunc{setLOC, true},
	TypeLP:         parserFunc{setLP, false},
	TypeMB:         parserFunc{setMB, false},
	TypeMD:         parserFunc{setMD, false},
	TypeMF:         parserFunc{setMF, false},
	TypeMG:         parserFunc{setMG, false},
	TypeMINFO:      parserFunc{setMINFO, false},
	TypeMR:         parserFunc{setMR, false},
	TypeMX:         parserFunc{setMX, false},
	TypeNAPTR:      parserFunc{setNAPTR, false},
	TypeNID:        parserFunc{setNID, false},
	TypeNIMLOC:     parserFunc{setNIMLOC, true},
	TypeNINFO:      parserFunc{setNINFO, true},
	TypeNSAP:       parserFunc{setNSAP, true},
	TypeNSAPPTR:    parserFunc{setNSAPPTR, false},
	TypeNSEC3PARAM: parserFunc{setNSEC3PARAM, false},
	TypeNSEC3:      parserFunc{setNSEC3, true},
	TypeNSEC:       parserFunc{setNSEC, true},
	TypeNS:         parserFunc{setNS, false},
	TypeOPENPGPKEY: parserFunc{setOPENPGPKEY, true},
	TypePTR:        parserFunc{setPTR, false},
	TypePX:         parserFunc{setPX, false},
	TypeRKEY:       parserFunc{setRKEY, true},
	TypeRP:         parserFunc{setRP, false},
	TypeRRSIG:      parserFunc{setRRSIG, true},
	TypeRT:         parserFunc{setRT, false},
	TypeSOA:        parserFunc{setSOA, false},
	TypeSPF:        parserFunc{setSPF, true},
	TypeSRV:        parserFunc{setSRV, false},
	TypeSSHFP:      parserFunc{setSSHFP, false},
	TypeTALINK:     parserFunc{setTALINK, false},
	TypeTA:         parserFunc{setTA, true},
	TypeTLSA:       parserFunc{setTLSA, true},
	TypeTXT:        parserFunc{setTXT, true},
	TypeUID:        parserFunc{setUID, false},
	TypeUINFO:      parserFunc{setUINFO, true},
	TypeURI:        parserFunc{setURI, true},
	TypeWKS:        parserFunc{setWKS, true},
	TypeX25:        parserFunc{setX25, false},
}
