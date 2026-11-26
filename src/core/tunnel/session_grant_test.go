package tunnel

import ("fmt";"testing")

func TestSessionAdmissionInitialGrant(t *testing.T) {
	a:=&sessionAdmission{limit:3,owners:map[string]int{}}
	for i:=0;i<15;i++ {a.accounts=append(a.accounts,fmt.Sprintf("a%02d",i))}
	r1,err:=a.acquire("z");if err!=nil{t.Fatal(err)};defer r1()
	g1:=a.grant("z");if len(g1)!=8{t.Fatal(g1)}
	r2,err:=a.acquire("a");if err!=nil{t.Fatal(err)};defer r2()
	g2:=a.grant("a");if len(g2)!=7{t.Fatal(g2)}
	seen:=map[string]bool{}
	for _,g:=range [][]string{g1,g2}{for _,v:=range g{if seen[v]{t.Fatalf("unexpected overlap %s",v)};seen[v]=true}}
	if len(seen)!=15{t.Fatal(seen)}
	replacement,err:=a.acquire("a");if err!=nil{t.Fatal(err)};r2();defer replacement()
	if len(a.grant("a"))!=7{t.Fatal("replacement lost grant")}
	replacement();if len(a.grant("a"))!=0{t.Fatal("release retained grant")}
	r1();r3,err:=a.acquire("a");if err!=nil{t.Fatal(err)};defer r3();if len(a.grant("a"))!=8{t.Fatal("single user grant wrong")}
}
