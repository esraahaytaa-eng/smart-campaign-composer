package db

import (
	"fmt"
	"math/rand/v2"
	"strings"
)

// Seed data is generated from a fixed RNG seed (D28). Every date is stored as
// seconds before the reference time, so the data is the same whenever it runs.

const (
	userCount = 3000
	day       = int64(24 * 60 * 60)
)

type genUser struct {
	id                   int64
	firstName, lastName  string // lastName "" is stored as NULL (D12)
	email, phone, city   string
	language             string
	smsOptIn, emailOptIn bool
	signedUpAgo          int64 // seconds before the reference time
	profile              string
}

type genItem struct {
	category   string
	quantity   int
	priceCents int64
}

type genOrder struct {
	id, userID int64
	ago        int64
	items      []genItem
}

type genCart struct {
	userID int64
	ago    int64
	item   genItem
}

type dataset struct {
	users  []genUser
	orders []genOrder
	carts  []genCart
}

// history fills one user's orders and cart from the profile's own date ranges.
type history func(g *generator, u *genUser)

var profiles = []struct {
	name   string
	weight int // out of 100
	fill   history
}{
	// Cart in the last 30 days, no order since: the CASE.md example.
	{"cart_abandoner", 15, func(g *generator, u *genUser) {
		g.orders(u, g.between(0, 2), 31, 360)
		g.carts(u, g.between(1, 3), 0, 30)
	}},
	{"recent_buyer", 20, func(g *generator, u *genUser) {
		g.orders(u, g.between(1, 3), 0, 30)
		g.orders(u, g.between(0, 3), 30, 360)
		g.carts(u, g.chance(50), 0, 60)
	}},
	{"lapsed_buyer", 15, func(g *generator, u *genUser) {
		g.orders(u, g.between(1, 4), 91, 360)
		g.carts(u, g.chance(30), 91, 180)
	}},
	{"vip", 7, func(g *generator, u *genUser) {
		g.orders(u, g.between(8, 20), 0, 360)
		g.carts(u, g.chance(30), 0, 30)
	}},
	{"new_signup", 10, func(g *generator, u *genUser) {
		u.signedUpAgo = g.secondsAgo(0, 30)
		g.ordersSince(u, g.chance(40))
		g.cartsSince(u, g.chance(50)*g.between(1, 2))
	}},
	{"browser", 13, func(g *generator, u *genUser) {
		g.carts(u, g.between(1, 4), 0, 180)
	}},
	{"inactive", 20, func(g *generator, u *genUser) {}},
}

type category struct {
	code           string
	minTRY, maxTRY int64
}

// Codes match the categories rows in the first migration.
var categories = []category{
	{"electronics", 300, 12000},
	{"fashion", 100, 2500},
	{"home", 80, 4000},
	{"beauty", 50, 1200},
	{"sports", 100, 3000},
	{"books", 30, 400},
}

type generator struct {
	r       *rand.Rand
	data    dataset
	orderID int64
}

func generate(seed int64) dataset {
	g := &generator{r: rand.New(rand.NewPCG(uint64(seed), uint64(seed)))}
	for i := 1; i <= userCount; i++ {
		u := g.user(int64(i))
		g.pickProfile(&u)
		g.data.users = append(g.data.users, u)
	}
	return g.data
}

func (g *generator) user(id int64) genUser {
	first := firstNames[g.r.IntN(len(firstNames))]
	last := lastNames[g.r.IntN(len(lastNames))]
	if g.r.IntN(100) < 3 {
		last = ""
	}
	local := emailLocalPart(first, last)
	u := genUser{
		id:          id,
		firstName:   first,
		lastName:    last,
		email:       fmt.Sprintf("%s.%d@example.com", local, id),
		phone:       fmt.Sprintf("+90555%07d", id),
		city:        cities[g.r.IntN(len(cities))],
		language:    "tr",
		signedUpAgo: g.secondsAgo(365, 1095),
	}
	if g.r.IntN(100) < 10 {
		u.language = "en"
	}
	switch c := g.r.IntN(100); {
	case c < 55:
		u.smsOptIn, u.emailOptIn = true, true
	case c < 70:
		u.smsOptIn = true
	case c < 85:
		u.emailOptIn = true
	}
	return u
}

func (g *generator) pickProfile(u *genUser) {
	n := g.r.IntN(100)
	for _, p := range profiles {
		if n < p.weight {
			u.profile = p.name
			p.fill(g, u)
			return
		}
		n -= p.weight
	}
	panic("profile weights must sum to 100")
}

// orders adds n orders between minDays and maxDays before the reference time.
func (g *generator) orders(u *genUser, n int, minDays, maxDays int64) {
	for range n {
		g.addOrder(u, g.secondsAgo(minDays, maxDays))
	}
}

// ordersSince adds n orders after the user's signup (new signups).
func (g *generator) ordersSince(u *genUser, n int) {
	for range n {
		g.addOrder(u, g.r.Int64N(u.signedUpAgo+1))
	}
}

func (g *generator) addOrder(u *genUser, ago int64) {
	g.orderID++
	o := genOrder{id: g.orderID, userID: u.id, ago: ago}
	for range g.between(1, 3) {
		o.items = append(o.items, g.item())
	}
	g.data.orders = append(g.data.orders, o)
}

func (g *generator) carts(u *genUser, n int, minDays, maxDays int64) {
	for range n {
		g.data.carts = append(g.data.carts, genCart{userID: u.id, ago: g.secondsAgo(minDays, maxDays), item: g.item()})
	}
}

func (g *generator) cartsSince(u *genUser, n int) {
	for range n {
		g.data.carts = append(g.data.carts, genCart{userID: u.id, ago: g.r.Int64N(u.signedUpAgo + 1), item: g.item()})
	}
}

func (g *generator) item() genItem {
	c := categories[g.r.IntN(len(categories))]
	return genItem{category: c.code, quantity: g.between(1, 2), priceCents: (c.minTRY + g.r.Int64N(c.maxTRY-c.minTRY+1)) * 100}
}

// secondsAgo is a uniform offset in [minDays, maxDays) days.
func (g *generator) secondsAgo(minDays, maxDays int64) int64 {
	return minDays*day + g.r.Int64N((maxDays-minDays)*day)
}

func (g *generator) between(lo, hi int) int { return lo + g.r.IntN(hi-lo+1) }

// chance returns 1 with the given percent probability, else 0.
func (g *generator) chance(percent int) int {
	if g.r.IntN(100) < percent {
		return 1
	}
	return 0
}

var asciiFold = strings.NewReplacer(
	"Ç", "c", "ç", "c", "Ğ", "g", "ğ", "g", "İ", "i", "I", "i", "ı", "i",
	"Ö", "o", "ö", "o", "Ş", "s", "ş", "s", "Ü", "u", "ü", "u", " ", ".",
)

// emailLocalPart turns "Çağrı", "Işık Öztürk" into "cagri.isik.ozturk".
func emailLocalPart(first, last string) string {
	name := first
	if last != "" {
		name += " " + last
	}
	return strings.ToLower(asciiFold.Replace(name))
}
