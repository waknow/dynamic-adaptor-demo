package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
)

func main() {
	var addr, confFile string

	flag.StringVar(&addr, "addr", ":8080", "listen addr")
	flag.StringVar(&confFile, "conf", "config.json", "config file path")
	flag.Parse()

	f, err := os.OpenFile(confFile, os.O_RDONLY, os.ModePerm)
	if err != nil {
		log.Fatalln("open config file", err.Error())
	}
	defer f.Close()

	var conf Config
	decoder := json.NewDecoder(f)
	decoder.UseNumber()
	err = decoder.Decode(&conf)
	if err != nil {
		log.Fatalln("decode config file failed", err.Error())
	}
	f.Close()

	http.HandleFunc("/statistics", statisticHandler)

	for _, protocol := range conf.Protocols {
		log.Println("add", protocol.Method, protocol.Path)
		validFuncs := map[string]validFunc{}
		for _, arg := range protocol.Args {
			validFuncs[arg.Name], err = generateValidFunc(arg)
			if err != nil {
				log.Println("generate arg valid failed", arg.Name, err)
			}
		}
		http.HandleFunc(protocol.Path, newHandleFunc(protocol.Method, protocol.Args, validFuncs))
	}

	if err = http.ListenAndServe(addr, nil); err != nil {
		log.Fatalln(err)
	}
}

var statistic = &Statistic{}

func statisticHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	statistic.Inc(fmt.Sprintf("%s.request", r.RequestURI), 1)
	fmt.Fprint(w, statistic.Json())
}

func newHandleFunc(method string, args []*Arg, validFuncs map[string]validFunc) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		statistic.Inc(fmt.Sprintf("%s.request", r.RequestURI), 1)
		statistic.Inc(fmt.Sprintf("%s.total_size", r.RequestURI), int(r.ContentLength))
		if method != r.Method {
			m{
				"code": Code_Err_Request,
				"msg":  fmt.Sprintf("mothod '%s' is not supported", r.Method),
			}.Write(w)
			return
		}
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()

		data := map[string]interface{}{}
		if err := decoder.Decode(&data); err != nil {
			m{
				"code": Code_Err_Request,
				"msg":  err.Error(),
			}.Write(w)
			return
		}

		valid := m{}
		invalid := m{}
		for _, arg := range args {
			v, ok := data[arg.Name]
			if !ok {
				invalid[arg.Name] = "<missed>"
				statistic.Inc(fmt.Sprintf("%s.args.missed", r.RequestURI), 1)
			} else {
				validFunc := validFuncs[arg.Name]
				if validFunc != nil {
					if err := validFunc(v); err != nil {
						invalid[arg.Name] = fmt.Sprintf("<err: %s>", err.Error())
						statistic.Inc(fmt.Sprintf("%s.args.invalid", r.RequestURI), 1)
					} else {
						statistic.Inc(fmt.Sprintf("%s.args.valid", r.RequestURI), 1)
						valid[arg.Name] = v
					}
				} else {
					valid[arg.Name] = v
					statistic.Inc(fmt.Sprintf("%s.args.valid", r.RequestURI), 1)
				}
			}
		}
		m{
			"code":    Code_OK,
			"valid":   valid,
			"invalid": invalid,
		}.Write(w)
	}
}

const (
	Code_OK           = 0
	Code_Err_Request  = 10000
	Code_Err_Internal = 20000
)

type m map[string]interface{}

func (m m) Write(w http.ResponseWriter) error {
	w.Header().Add("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(m)
}

//Config ...
type Config struct {
	Protocols []*Protocol
}

//Protocol ...
type Protocol struct {
	Path   string
	Method string
	Args   []*Arg
}

type ArgType int

const (
	//Int
	Int ArgType = 1
	//String
	String ArgType = 2
	//Bool
	Bool ArgType = 3
)

type Arg struct {
	Name         string
	Type         ArgType
	Restrictions Restrictions
}

type Restrictions map[string]interface{}

func generateValidFunc(arg *Arg) (validFunc, error) {
	switch arg.Type {
	case String:
		return generateStringValidFunc(arg)
	case Int:
		return generateIntValidFunc(arg)
	case Bool:
		return generateBoolValidFunc(arg)
	default:
		log.Println("arg", arg.Name, "argType", arg.Type, "currently not supported")
	}
	return nil, nil
}

type validFunc func(v interface{}) error
type stringValidFunc func(s string) error
type intValidFunc func(i int64) error
type boolValidFunc func(b bool) error

func newStringTypeValidFunc(validFuncs ...stringValidFunc) validFunc {
	return func(v interface{}) error {
		if v == nil {
			return nil
		}
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("not string")
		}
		for _, validFunc := range validFuncs {
			if err := validFunc(s); err != nil {
				return err
			}
		}
		return nil
	}
}

func newIntTypeValidFunc(validFuncs ...intValidFunc) validFunc {
	return func(v interface{}) error {
		if v == nil {
			return nil
		}
		n, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("not number")
		}
		i, err := n.Int64()
		if err != nil {
			return err
		}
		for _, validFunc := range validFuncs {
			if err := validFunc(i); err != nil {
				return err
			}
		}
		return nil
	}
}

func newBoolTypeValidFunc(validFuncs ...boolValidFunc) validFunc {
	return func(v interface{}) error {
		if v == nil {
			return nil
		}
		_, ok := v.(bool)
		if !ok {
			return fmt.Errorf("not bool")
		}
		return nil
	}
}

func generateStringValidFunc(arg *Arg) (validFunc, error) {
	stringValidFuncs := []stringValidFunc{}
	for name, restriction := range arg.Restrictions {
		switch name {
		case "length":
			if validFunc, err := generateStringLengthValidFunc(restriction); err != nil {
				return nil, err
			} else if validFunc != nil {
				stringValidFuncs = append(stringValidFuncs, validFunc)
			} else {
				log.Println("valid func for", arg.Name, "of length can not be applied, may max and min not found")
			}
		default:
			log.Println("arg", arg.Name, "restriction of", name, "currently not suppored")
		}
	}
	return newStringTypeValidFunc(stringValidFuncs...), nil
}

func generateIntValidFunc(arg *Arg) (validFunc, error) {
	intValidFuncs := []intValidFunc{}
	for name, restriction := range arg.Restrictions {
		switch name {
		case "range":
			if validFunc, err := generateIntRangeValidFunc(restriction); err != nil {
				return nil, err
			} else if validFunc != nil {
				intValidFuncs = append(intValidFuncs, validFunc)
			} else {
				log.Println("valid func for", arg.Name, "of length can not be applied, may max and min not found")
			}
		default:
			log.Println("arg", arg.Name, "restriction of", name, "currently not suppored")
		}
	}
	return newIntTypeValidFunc(intValidFuncs...), nil
}

func generateBoolValidFunc(arg *Arg) (validFunc, error) {
	return newBoolTypeValidFunc(), nil
}

//string length
func generateStringLengthValidFunc(restriction interface{}) (stringValidFunc, error) {
	if restriction == nil {
		return nil, nil
	}
	m := restriction.(map[string]interface{})
	max, maxExisted := m["max"]
	min, minExisted := m["min"]
	var maxV, minV int64
	var err error
	if maxExisted && minExisted {
		if maxV, err = (max.(json.Number)).Int64(); err != nil {
			return nil, err
		}
		if minV, err = (min.(json.Number)).Int64(); err != nil {
			return nil, err
		}
		return func(s string) error {
			l := int64(len(s))
			if l < minV {
				return fmt.Errorf("min: %d, current: %d", minV, l)
			}
			if l > maxV {
				return fmt.Errorf("max: %d, current: %d", maxV, l)
			}
			return nil
		}, nil
	} else if maxExisted {
		if maxV, err = (max.(json.Number)).Int64(); err != nil {
			return nil, err
		}
		return func(s string) error {
			l := int64(len(s))
			if l > maxV {
				return fmt.Errorf("max: %d, current: %d", maxV, l)
			}
			return nil
		}, nil
	} else if minExisted {
		if minV, err = (min.(json.Number)).Int64(); err != nil {
			return nil, err
		}
		return func(s string) error {
			l := int64(len(s))
			if l < minV {
				return fmt.Errorf("min: %d, current: %d", minV, l)
			}
			return nil
		}, nil
	}
	return nil, nil
}

//int range
func generateIntRangeValidFunc(restriction interface{}) (intValidFunc, error) {
	if restriction == nil {
		return nil, nil
	}
	m := restriction.(map[string]interface{})
	max, maxExisted := m["max"]
	min, minExisted := m["min"]
	var maxV, minV int64
	var err error
	if maxExisted && minExisted {
		if maxV, err = (max.(json.Number)).Int64(); err != nil {
			return nil, err
		}
		if minV, err = (min.(json.Number)).Int64(); err != nil {
			return nil, err
		}
		return func(i int64) error {
			if i < minV {
				return fmt.Errorf("min: %d, current: %d", minV, i)
			}
			if i > maxV {
				return fmt.Errorf("max: %d, current: %d", maxV, i)
			}
			return nil
		}, nil
	} else if maxExisted {
		if maxV, err = (max.(json.Number)).Int64(); err != nil {
			return nil, err
		}
		return func(i int64) error {
			if i > maxV {
				return fmt.Errorf("max: %d, current: %d", maxV, i)
			}
			return nil
		}, nil
	} else if minExisted {
		if minV, err = (min.(json.Number)).Int64(); err != nil {
			return nil, err
		}
		return func(i int64) error {
			if i < minV {
				return fmt.Errorf("min: %d, current: %d", minV, i)
			}
			return nil
		}, nil
	}
	return nil, nil
}

type Statistic struct {
	data  map[string]interface{}
	mutex sync.Mutex
}

func (s *Statistic) Inc(path string, delta int) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.data == nil {
		s.data = map[string]interface{}{}
	}

	parts := strings.Split(path, ".")
	pathParts := parts[:len(parts)-1]
	valueName := parts[len(parts)-1]
	// log.Println("pathParts valueName", pathParts, valueName)
	ok := false
	cur := s.data
	var next map[string]interface{}
	passedParts := []string{}
	for _, part := range pathParts[:] {
		passedParts = append(passedParts, part)
		// log.Println(passedParts, part)
		i, ok := cur[part]
		if !ok {
			next = map[string]interface{}{}
			cur[part] = next
		} else {
			next, ok = i.(map[string]interface{})
			if !ok {
				log.Printf("%v %t", passedParts, i)
				return fmt.Errorf("%s is a value", strings.Join(passedParts, "."))
			}
		}
		cur = next
	}

	i := cur[valueName]
	var value int
	if i == nil {
		value = delta
	} else if value, ok = i.(int); !ok {
		log.Printf("%v %v", passedParts, i)
		return fmt.Errorf("not value which you want to inc")
	} else {
		value += delta
	}
	cur[valueName] = value
	// log.Println(s.data)
	// log.Println(cur)
	return nil
}

func (s *Statistic) Json() string {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	bs, err := json.Marshal(s.data)
	if err != nil {
		panic(err)
	}
	return string(bs)
}
