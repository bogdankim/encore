package bridge

// Word lists for the demo library. Everything here is invented; combinations
// are built by fakelib.go.

var (
	wAdj = []string{
		"Golden", "Quiet", "Electric", "Paper", "Silver", "Hollow", "Velvet", "Midnight",
		"Northern", "Distant", "Amber", "Glass", "Wild", "Gentle", "Broken", "Endless",
		"Faded", "Little", "Lonely", "Crimson", "Pale", "Bright", "Slow", "Sudden",
		"Secret", "Tender", "Restless", "Burning", "Frozen", "Blue", "Copper", "Ivory",
		"Hidden", "Open", "Lost", "Soft", "Warm", "Strange", "Sweet", "Second", "Last",
		"Early", "Western", "Summer", "Winter", "Autumn", "Static", "Lucid", "Tidal",
		"Sleepless", "Neon", "Salt", "Cinnamon", "Sunday", "Analog", "Weightless",
	}
	wNoun = []string{
		"Light", "River", "Harbor", "Garden", "Mirror", "Signal", "Coast", "Window",
		"Horizon", "Engine", "Echo", "Lantern", "Satellite", "Ocean", "Highway", "Letter",
		"Island", "Shadow", "Season", "Fever", "Wave", "Heart", "Machine", "Weather",
		"Station", "Kingdom", "Morning", "Evening", "Avenue", "Canyon", "Desert", "Forest",
		"Meadow", "Orchard", "Compass", "Anchor", "Thunder", "Rain", "Dust", "Smoke",
		"Silk", "Stone", "Bloom", "Feather", "Arrow", "Ember", "Prism", "Tide", "Daylight",
		"Moonlight", "Skyline", "Parade", "Carousel", "Postcard", "Cassette", "Radio",
		"Telephone", "Lighthouse", "Boulevard", "Balcony", "Rooftop", "Motel", "Diner",
		"Arcade", "Glow", "Shoreline", "Driveway", "Porch", "Ferris Wheel", "Paper Moon",
	}
	wPlural = []string{
		"Lanterns", "Wolves", "Rivers", "Satellites", "Strangers", "Daydreams", "Echoes",
		"Parades", "Machines", "Sparrows", "Foxes", "Pilots", "Painters", "Saints",
		"Ghosts", "Comets", "Horses", "Tigers", "Owls", "Swallows", "Kites", "Sailors",
		"Dreamers", "Islands", "Weekends", "Colors", "Mountains", "Lights", "Hearts",
		"Antlers", "Orchards", "Telescopes", "Lovers", "Tourists", "Neighbors",
	}
	wVerb = []string{
		"Run", "Fall", "Hold", "Wait", "Call", "Dance", "Drive", "Breathe", "Wake", "Stay",
		"Burn", "Follow", "Carry", "Forget", "Remember", "Leave", "Find", "Chase", "Keep",
		"Turn", "Fly", "Shine", "Move", "Sleep", "Dream", "Return", "Belong", "Let Go",
	}
	wPlace = []string{
		"Lisbon", "Osaka", "Berlin", "Marseille", "Reykjavík", "Montréal", "Tbilisi",
		"Kyoto", "Valparaíso", "Brooklyn", "Oakland", "Glasgow", "Ostend", "Tangier",
		"Helsinki", "Seville", "Havana", "Lagos", "Seoul", "Taipei", "Santa Fe", "Memphis",
		"Kingston", "Bristol", "Copenhagen", "Ljubljana", "Porto", "Trieste", "Cádiz",
		"Duluth", "Big Sur", "Marfa",
	}
	wFirst = []string{
		"Mara", "Theo", "Ines", "Jonah", "Lila", "Felix", "Noor", "Arlo", "Hana", "Elias",
		"Priya", "Oskar", "Juno", "Marisol", "Tobias", "Wren", "Ada", "Rafael", "Selin",
		"Mateo", "Ivy", "Nico", "Saoirse", "Dario", "Leni", "Kofi", "Amara", "Bastian",
		"Yara", "Callum", "Esme", "Rowan", "Talia", "Emil", "Sunniva", "Ezra", "June",
		"Otis", "Margot", "Silas", "Clementine", "Ruben", "Vera", "Hollis", "Delphine",
		"Idris", "Maeve", "Linus", "Paloma", "Cass", "Imogen", "Teodor", "Lux",
	}
	wFirstAccented = []string{"Céline", "Björn", "Zoë", "Joaquín", "Søren", "Anaïs", "Mireille", "Hélène", "Íñigo", "Łucja", "Dagný", "Thérèse"}
	wLast          = []string{
		"Lindqvist", "Halvorsen", "Okafor", "Marchetti", "Arnaud", "Ekdahl", "Castellanos",
		"Whitlock", "Moreau", "Nakamura", "Petrović", "Haddad", "Brennan", "Sorensen",
		"Vasquez", "Albright", "Kowalczyk", "Duarte", "Fontaine", "Ashby", "Mercer",
		"Calloway", "Laurent", "Ferreira", "Novak", "Okoro", "Varga", "Lindgren",
		"Abernathy", "Rosales", "Hartley", "Quinlan", "Delacroix", "Sato", "Bergström",
		"Moreno", "Kaplan", "Thorne", "Valentine", "Sinclair", "Larkin", "Åkesson",
		"Ibáñez", "Dvořák-Hale", "Muñoz", "Østergaard",
	}
	wSylA = []string{"Sol", "Mira", "Vel", "Kai", "Lumo", "Nova", "Oro", "Halo", "Ari", "Tali", "Rho", "Ondo", "Cyra", "Red", "Vanta", "Ilo", "Saro", "Brio"}
	wSylB = []string{"enne", "matic", "ora", "lux", "vane", "ia", "phonic", "tone", "wave", "field", "haus", "line", "ette", "dale", "mont"}

	wHall  = []string{"Ballroom", "Opera House", "Theatre", "Music Hall", "Roundhouse", "Pavilion", "Social Club"}
	wTail  = []string{"Comes Down", "Breaks", "Turns", "Goes Quiet", "Lets Go", "Calls Your Name", "Runs Dry", "Wakes Up"}
	wGenre = []string{
		"Alternative", "Indie Pop", "Electronic", "Hip-Hop/Rap", "R&B/Soul", "Jazz", "Rock",
		"Pop", "Singer/Songwriter", "Ambient", "Folk", "Classical", "Dance", "Country",
		"Metal", "Latin", "Reggae", "Blues",
	}
	wGenreWeight = []int{14, 12, 10, 6, 7, 6, 9, 8, 6, 4, 5, 3, 4, 2, 2, 3, 1, 2}

	wLongTitles = []string{
		"The Night We Drove All the Way to %s and Didn't Say a Word",
		"Everything I Never Told You About the Summer in %s",
		"If You Ever Find Yourself Back in %s, Leave the Light On for Me",
		"Somewhere Between %s and the Last Train Home",
	}
	wLongAlbums = []string{
		"Everything We Built on the Way Down to the Sea",
		"Songs for People Who Stay Up Too Late and Wake Up Too Early",
		"A Short History of the Weather in Our Old Neighborhood",
		"Nothing Here Is Permanent, Not Even the Mountains",
	}
	wRoman = []string{"I", "II", "III", "IV", "V", "VI", "VII"}

	// Classical
	wForm      = []string{"Symphony", "Piano Sonata", "String Quartet", "Violin Concerto", "Nocturne", "Étude", "Cello Suite", "Serenade"}
	wKey       = []string{"C Minor", "E-Flat Major", "A Major", "F-Sharp Minor", "D Major", "B-Flat Minor", "G Major", "E Minor"}
	wTempo     = []string{"Allegro con brio", "Andante cantabile", "Adagio", "Presto", "Scherzo. Allegro vivace", "Largo", "Allegretto", "Rondo. Allegro ma non troppo", "Moderato"}
	wEnsembleA = []string{"Lindqvist", "Halvorsen", "Marchetti", "Ostrava", "Vintersjö", "Bellamy", "Rosenhain"}
	wEnsembleB = []string{"String Quartet", "Chamber Orchestra", "Ensemble", "Piano Trio", "Philharmonic Players"}

	// Non-Latin artists, with romanized sort names and titles in their script.
	nlArtists = []struct{ name, sort, genre, script string }{
		{"森 楓", "Mori Kaede", "J-Pop", "ja"},
		{"雨宮ゆい", "Amamiya Yui", "J-Pop", "ja"},
		{"東京湾ラジオ", "Tokyo-wan Radio", "Alternative", "ja"},
		{"星野 灯", "Hoshino Akari", "Singer/Songwriter", "ja"},
		{"夜光バス", "Yakou Bus", "Electronic", "ja"},
		{"Ночные Огни", "Nochnye Ogni", "Alternative", "ru"},
		{"Лиза Орлова", "Orlova Liza", "Pop", "ru"},
		{"Дальний Свет", "Dalniy Svet", "Rock", "ru"},
		{"윤서하", "Yoon Seoha", "Pop", "ko"},
		{"푸른 새벽 밴드", "Pureun Saebyeok Band", "Indie Pop", "ko"},
	}
	nlTitles = map[string][]string{
		"ja": {"夏の終わり", "雨のち晴れ", "星空ドライブ", "さよならの向こう側", "青い鳥", "東京の夜", "ひとりごと", "海辺の手紙", "まわる季節", "午前二時", "光の中へ", "帰り道", "Midnight 東京", "君のいない部屋", "白い朝", "ネオンの海"},
		"ru": {"Звёзды", "Последний трамвай", "Город спит", "Тёплый ветер", "Белый шум", "Над рекой", "Северное сияние", "Без тебя", "Дорога домой", "Летняя ночь", "Синий час", "Старое радио"},
		"ko": {"여름밤", "푸른 새벽", "너의 계절", "별이 빛나는 밤", "오래된 노래", "비 오는 날", "우리의 거리", "첫눈"},
	}
	nlAlbums = map[string][]string{
		"ja": {"ひかり", "東京ノスタルジア", "季節の手紙", "夜明け前", "まちの音", "青の時代"},
		"ru": {"Ночной город", "Свет в окне", "Север", "Время перемен"},
		"ko": {"계절의 끝", "새벽 네 시", "푸른 방"},
	}
	nlLines = map[string][]string{
		"ja": {"夜明けまで歩いていこう", "君の声が遠くで聞こえる", "遠い街の灯りが揺れる", "忘れないでいてね", "風が窓をたたいている", "ふたりの帰り道", "もう一度だけ笑って", "季節がまためぐる", "空の色が変わるまで", "言葉にならない想い"},
		"ru": {"Мы идём по ночному городу", "Не забывай меня", "Где-то горит свет", "Ветер над рекой", "До самого утра", "Ты смотришь в окно", "Всё будет иначе", "Снова зима"},
		"ko": {"새벽이 오기 전에", "너의 목소리가 들려", "멀리 있는 불빛", "잊지 말아줘", "다시 한 번 웃어줘", "계절이 지나가도"},
	}

	// Lyrics lines are assembled from these fragments.
	lyrOpen = []string{
		"I left the", "We followed the", "You kept the", "Somebody turned the", "Every night the",
		"Under the", "Nobody saw the", "Carry me past the", "Hold on to the", "Wait for the",
		"I still hear the", "We painted the", "Don't forget the", "Meet me by the", "I dreamed about the",
	}
	lyrEnd = []string{
		"on the way back home", "until the morning", "across the water", "in the rearview mirror",
		"like it was yesterday", "and never looked back", "when the city sleeps", "one more time",
		"before it fades", "where the river bends", "out on the highway", "in a borrowed coat",
		"with the windows down", "till the static clears", "and the radio low",
	}
	lyrHook = []string{
		"Oh, stay a little longer", "We were young and the night was ours", "Say it like you mean it",
		"Let the light come in", "Nothing's gonna slow us down", "Take me where the summer goes",
		"I'll be the one who waits", "All the way, all the way", "Don't you let me go tonight",
		"We can make it if we run",
	}
)
