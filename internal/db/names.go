package db

// Fictional Turkish names. Combined at random, they belong to no real person.

var firstNames = []string{
	"Ayşe", "Fatma", "Zeynep", "Elif", "Merve", "Büşra", "Esra", "Gül", "Hülya", "İpek",
	"Özge", "Şeyma", "Çağla", "Derya", "Ebru", "Selin", "Deniz", "Melis", "Nur", "Tuğba",
	"Mehmet", "Mustafa", "Ahmet", "Ali", "Hüseyin", "Hasan", "İbrahim", "İsmail", "Murat", "Ömer",
	"Can", "Emre", "Burak", "Çağrı", "Oğuz", "Barış", "Kerem", "Serkan", "Uğur", "Yiğit",
}

var lastNames = []string{
	"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir",
	"Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Kara", "Koç", "Kurt", "Özkan", "Şimşek",
	"Polat", "Erdoğan", "Güneş", "Aksoy", "Turan", "Işık", "Bulut", "Tekin", "Ünal", "Güler",
}

// Repeats weight the draw toward larger cities, İstanbul most of all.
var cities = []string{
	"İstanbul", "İstanbul", "İstanbul", "İstanbul", "İstanbul", "İstanbul", "İstanbul", "İstanbul", "İstanbul",
	"Ankara", "Ankara", "Ankara", "Ankara",
	"İzmir", "İzmir", "İzmir",
	"Bursa", "Bursa", "Antalya", "Antalya",
	"Adana", "Konya", "Gaziantep", "Kocaeli", "Mersin", "Kayseri", "Eskişehir", "Diyarbakır",
	"Samsun", "Trabzon", "Muğla",
}
