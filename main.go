package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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

	for _, protocol := range conf.Protocols {
		log.Println("add", protocol.Method, protocol.Path)
		validFuncs := map[string]validFunc{}
		for _, arg := range protocol.Args {
			validFuncs[arg.Name], err = generateValidFunc(arg.Type, arg.Restrictions)
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

func newHandleFunc(method string, args []*Arg, validFuncs map[string]validFunc) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
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
			} else {
				validFunc := validFuncs[arg.Name]
				if validFunc != nil {
					if err := validFunc(v); err != nil {
						invalid[arg.Name] = fmt.Sprintf("<err: %s>", err.Error())
					} else {
						valid[arg.Name] = v
					}
				} else {
					valid[arg.Name] = v
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

func generateValidFunc(argType ArgType, restrictions Restrictions) (validFunc, error) {
	switch argType {
	case String:
		return generateStringValidFunc(restrictions)
	default:
		log.Println("argType", argType, "currently not supported")
	}
	return nil, nil
}

func generateStringValidFunc(restrictions Restrictions) (validFunc, error) {
	stringValidFuncs := []stringValidFunc{}
	for name, restriction := range restrictions {
		switch name {
		case "length":
			if validFunc, err := generateStringLengthValidFunc(restriction); err != nil {
				return nil, err
			} else {
				stringValidFuncs = append(stringValidFuncs, validFunc)
			}
		default:
			log.Println("valid", name, "currently not suppored")
		}
	}
	return newStringTypeValidFunc(stringValidFuncs...), nil
}

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

type validFunc func(v interface{}) error
type stringValidFunc func(s string) error
type intValidFunc func(i int64) error

type stringTypeValidFunc func(validFuncs ...stringValidFunc) validFunc
type stringMaxLengthValidFunc func(max int) stringValidFunc

type intTypeValidFunc func(validFuncs ...intValidFunc) validFunc
type intMaxValidFunc func(max int64) intValidFunc
type intMinValidFunc func(min int64) intValidFunc
type intRangeValidFunc func(min, max int64) intValidFunc

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
