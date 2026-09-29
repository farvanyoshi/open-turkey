package lock

import "strings"

// wordList is the vocabulary for the unlock challenge: common, inoffensive,
// lowercase English words of 3–8 letters. Drawn in random order, they are easy
// to read and type but impossible to anticipate, so the challenge costs time
// rather than luck.
var wordList = strings.Fields(`
able about above accept across act add admit adult afraid after again age
agent agree ahead air allow almost alone along already also always amount
angle animal answer any appear apple area argue arm army around arrive art
artist ask attack aunt autumn avoid awake away baby back bad bag bake ball
band bank base basket bath beach bean bear beauty become bed bee before begin
behind bell below belt bench best better between bicycle big bird birth bit
bitter black blade blank blanket blind block blood blue board boat body boil
bone book border born borrow boss both bottle bottom bowl box brain branch
brave bread break breath brick bridge brief bright bring broad brother brown
brush bucket build bundle burn bus busy butter button buy cabin cable cake
call calm camera camp canal candle candy cap car card care carpet carry case
castle cat catch cattle cause ceiling cell center chain chair chalk chance
change chapter charge chase cheap check cheese chef cherry chest chicken chief
child choice choose church circle city claim class clean clear clever cliff
climb clock close cloth cloud clown coach coast coat coffee coin cold collar
color column comb come comfort common copper copy corn corner cost cotton
couch count country course cousin cover cow crack craft crane crash cream
create crisp crop cross crowd crown cup cure curious current curtain curve
cushion custom cut cycle daily damage dance danger dark date daughter day deal
dear debate decide deep deer degree delay depth desert design desk detail
diamond diary dinner direct dirt dish distant divide doctor dog dollar door
double doubt down dozen draft dragon drama draw dream dress drift drink drive
drop drum dry duck dust duty eager eagle early earn earth east easy eat echo
edge effect effort egg eight either elbow elder eleven else empty end enemy
energy engine enjoy enough enter entire equal escape even evening event ever
every exact example except exit expect expert extra eye face fact factory fair
faith fall false family famous far farm fashion fast father fault favor fear
feather feel fence festival few field fifteen fight figure fill film final
find fine finger finish fire firm first fish five flag flame flat flavor fleet
flight float flock floor flour flower fluid fly focus fog fold follow food
foot force forest forget fork form fortune forward four fox frame free fresh
friend frog front frost fruit fuel full fun future gain gallery game garage
garden garlic gate gather gentle giant gift girl give glad glass globe glove
glow goat gold good grab grace grain grand grape grass gravel great green grey
grid ground group grow guard guess guest guide guitar habit hair half hall
hammer hand handle happen happy harbor hard harvest hat have head health heap
hear heart heat heavy height hello helmet help hen herb hero hidden high hill
history hobby hold hole holiday hollow home honest honey hope horn horse
hotel hour house human humble hundred hunger hunt hurry idea image inch
income index indoor infant ink insect inside island item jacket jar jazz jelly
jewel job join joke journey joy judge juice jump jungle junior just keen keep
kettle key kid kind king kitchen kite knee knife knock know label labor ladder
lady lake lamp land large laser last late laugh launch lawn layer lazy leader
leaf learn least leather leave left lemon lend length lesson letter level
library lift light like limit line linen lion liquid list listen little live
load loan local lock long look loose lose loud love low loyal lucky lunch
machine magic magnet maid mail main major make manage map maple marble march
market marry mask master match matter maybe meadow meal meaning measure meat
medal meet melody melt member memory mention menu mercy merit message metal
method middle might mild milk mill mind minute mirror mix model modern moment
money monkey month mood moon moral more morning moss most mother motion motor
mountain mouse mouth move movie much mud muscle museum music name narrow
nation nature near neat neck need needle nephew nerve nest net never new news
next nice night nine noble noise noon normal north nose note nothing notice
novel number nurse object ocean offer office often oil old olive once one
onion only open opinion orange orbit order organ other outer outside oven
over owner oxygen pace pack page paint pair palace palm panel paper parade
parent park part party pass past path patient pattern pause peace peach pearl
pencil people pepper perfect period person pet phone photo piano pick picnic
piece pig pillow pilot pine pink pipe pitch place plain plan planet plant
plate play please plenty plot pocket poem poet point polite pond pony pool
poor popular port pose post pot potato powder power praise prefer present
press pretty price pride print prize problem profit promise proof proper
proud public pull pumpkin pupil puppy purple push puzzle quarter queen quick
quiet quite rabbit race radio rail rain raise range rapid rare rate reach read
ready real reason record red reflect region relax remain remote repair repeat
reply report rescue rest result return reward rhythm rice rich ride right
ring ripe rise risk river road robot rock rocket roof room root rope rose
rough round route royal rubber rule ruler run rural sad saddle safe sail salad
salt same sample sand save scale scarf scene school science score screen sea
season seat second secret seed seek select sell send sense series serve
settle seven shade shadow shake shallow shape share sharp sheep sheet shelf
shell shelter shift shine ship shirt shoe shop shore short shoulder shout show
shower side sight sign silent silk silver simple since sing single sink
sister sit six size skate sketch ski skill skin skirt sky sleep slice slide
slight slow small smart smile smoke smooth snack snake snow soap soccer sock
soda soft soil solid some son song soon sort sound soup south space spare
speak special speed spell spend spice spider spirit splash spoon sport spot
spray spread spring square stable staff stage stair stamp stand star start
state station stay steady steam steel step stick still stone stool stop store
storm story stove straight strange straw stream street strong student study
style subject sudden sugar suit summer sun supper supply sure surface surprise
swamp sweet swift swim swing symbol table tail take talent talk tall tank tape
target task taste taxi tea teach team tear tell ten tennis tent term test text
thank theory thick thin thing think third thirty thread three throw thumb
ticket tide tidy tiger timber time tiny tired title toast today together
tomato tone tongue tonight tool tooth top topic total touch tough tour towel
tower town toy track trade traffic train travel tray treat tree trend trial
tribe trick trip truck true trumpet trust truth try tube tulip tune tunnel
turn turtle twelve twenty twice twin type uncle under unit until upper upset
urban use useful usual valley value van vapor velvet verse very vessel video
view village violin visit vivid voice volume vote voyage wagon wait wake walk
wall wander want warm wash watch water wave wax way wealth wear weather weave
week weight welcome well west wet whale wheat wheel whisper white whole wide
width wild will wind window wine wing winter wire wise wish wolf wonder wood
wool word work world worry worth wrap write yard year yellow young youth zebra
zero zone
`)
