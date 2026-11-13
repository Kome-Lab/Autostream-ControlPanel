package v3authorization

import (
 "bytes"
 "encoding/json"
 "io"
 "reflect"
 "strings"
 "unicode/utf8"
)

// These decoders only own the HTTP carrier. The immutable P-A request must also
// pass the separately configured canonical BindingResolver; no v2 decoder is used.
func DecodeIssueRequest(data []byte) (IssueRequest,error) {
 var v IssueRequest
 if strictDecode(data,&v)!=nil||len(v.OriginalRequest)==0||len(v.OriginalRequest)>64<<10||!validFrame(v.Frame){return IssueRequest{},ErrInvalid};return v,nil
}
func DecodeLookup(data []byte) (Lookup,error) {
 var v Lookup
 if strictDecode(data,&v)!=nil||!validLookup(v){return Lookup{},ErrInvalid};return v,nil
}
func strictDecode(data []byte,target any) error {
 if len(data)==0||len(data)>256<<10||!utf8.Valid(data){return ErrInvalid}
 d:=json.NewDecoder(bytes.NewReader(data));d.UseNumber()
 if err:=strictValue(d,reflect.TypeOf(target).Elem(),0);err!=nil{return ErrInvalid}
 if _,err:=d.Token();err!=io.EOF{return ErrInvalid}
 if json.Unmarshal(data,target)!=nil{return ErrInvalid};return nil
}
var rawType=reflect.TypeOf(json.RawMessage{})
func strictValue(d *json.Decoder,t reflect.Type,depth int) error {
 if depth>32{return ErrInvalid}
 tok,err:=d.Token();if err!=nil||tok==nil{return ErrInvalid}
 if t==rawType {return strictRaw(d,tok,depth)}
 switch t.Kind(){
 case reflect.Struct:
  if tok!=json.Delim('{'){return ErrInvalid}
  fields:=map[string]reflect.StructField{};required:=map[string]bool{}
  for n:=0;n<t.NumField();n++{f:=t.Field(n);tag:=f.Tag.Get("json");name:=strings.Split(tag,",")[0];if name==""{name=f.Name};fields[name]=f;required[name]=!strings.Contains(tag,",omitempty")}
  seen:=map[string]bool{}
  for d.More(){key,e:=d.Token();if e!=nil{return ErrInvalid};name,ok:=key.(string);f,exists:=fields[name];if !ok||!exists||seen[name]{return ErrInvalid};seen[name]=true;if strictValue(d,f.Type,depth+1)!=nil{return ErrInvalid}}
  close,e:=d.Token();if e!=nil||close!=json.Delim('}'){return ErrInvalid};for name,must:=range required{if must&&!seen[name]{return ErrInvalid}}
 case reflect.String:
  if _,ok:=tok.(string);!ok{return ErrInvalid}
 default:return ErrInvalid
 };return nil
}
func strictRaw(d *json.Decoder,tok json.Token,depth int) error {
 if depth>32||tok==nil{return ErrInvalid}
 switch tok {
 case json.Delim('{'):
  seen:=map[string]bool{};for d.More(){k,e:=d.Token();key,ok:=k.(string);if e!=nil||!ok||seen[key]{return ErrInvalid};seen[key]=true;v,e:=d.Token();if e!=nil||strictRaw(d,v,depth+1)!=nil{return ErrInvalid}};end,e:=d.Token();if e!=nil||end!=json.Delim('}'){return ErrInvalid}
 case json.Delim('['):
  for d.More(){v,e:=d.Token();if e!=nil||strictRaw(d,v,depth+1)!=nil{return ErrInvalid}};end,e:=d.Token();if e!=nil||end!=json.Delim(']'){return ErrInvalid}
 default:
  switch tok.(type){case string,json.Number,bool:default:return ErrInvalid}
 };return nil
}
