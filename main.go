package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"sort"

	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/customer"
	"github.com/stripe/stripe-go/v72/price"
)

type monthstats struct {
	revenue       float64
	subscriptions int
	key           string
}

type subscriptionStat struct {
	startDate time.Time
	revenue   float64
}

func main() {
	key := flag.String("key", "", "this is your stripe key, you may use an environment variable STRIPE_KEY.")
	flag.Parse()

	apiKey := os.Getenv("STRIPE_KEY")

	if *key != "" {
		apiKey = *key
	}

	if apiKey == "" {
		flag.Usage()
		return
	}

	stripe.Key = apiKey

	var mrr float64

	p := &stripe.CustomerListParams{}
	p.Filters.AddFilter("limit", "", "100")
	p.AddExpand("data.subscriptions")
	p.AddExpand("data.discount")
	months := make(map[string]monthstats) // [YEAR-MM]

	var subscriptions []subscriptionStat
	customers := customer.List(p)
	for customers.Next() {
		c := customers.Customer()

		if len(c.Subscriptions.Data) > 0 {
			subs := c.Subscriptions.Data

			for _, s := range subs {
				var subscriptionRevenue float64

				now := time.Now()
				nextYear := time.Date(now.Year()+1, now.Month(), 0, 0, 0, 0, 0, now.Location())

				if s.CancelAtPeriodEnd && time.Unix(s.CanceledAt, 0).Before(nextYear) {
					continue
				}
				if s.Status != stripe.SubscriptionStatusActive {
					continue
				}

				switch s.Plan.BillingScheme {
				case stripe.PlanBillingSchemeTiered:
					{
						priceParam := &stripe.PriceParams{}
						priceParam.AddExpand("tiers")
						plan, err := price.Get(s.Plan.ID, priceParam)
						if err != nil {
							log.Fatal(err)
						}
						if plan.TiersMode == stripe.PriceTiersModeGraduated {
							for i := int64(1); i <= s.Quantity; i++ { // Hopefully the array is sorted desc from upTo
								for _, tier := range plan.Tiers {
									if i <= tier.UpTo || tier.UpTo == 0 {
										subscriptionRevenue += tier.UnitAmountDecimal
										break
									}
								}
							}
						}
						if s.Plan.Interval == stripe.PlanIntervalYear {
							subscriptionRevenue = float64(subscriptionRevenue) / 12.0
						}
						// Two discounts can be at play, at subscription level and/or at the customer level
						subscriptionRevenue = applyDiscount(applyDiscount(subscriptionRevenue, s.Discount), c.Discount)

						subscriptions = append(subscriptions, subscriptionStat{
							startDate: time.Unix(s.StartDate, 0),
							revenue:   subscriptionRevenue / 100,
						})
					}
				default:
					log.Println("UNSUPPORTED BILLING SCHEME")
				}
			}
		} else {
			log.Println("this customer has no subs")
		}
	}

	for _, s := range subscriptions {
		mrr += s.revenue

		monthKey := s.startDate.Format("2006-01")

		if stats, ok := months[monthKey]; ok {
			stats.revenue += s.revenue
			stats.subscriptions++
			months[monthKey] = stats
		} else {
			stats = monthstats{subscriptions: 1, revenue: s.revenue, key: monthKey}
			months[monthKey] = stats
		}
	}

	var keys []string
	for _, v := range months {
		keys = append(keys, v.key)
	}

	sort.Strings(keys)

	fmt.Printf("MRR is\t%.2f\n", mrr)
	fmt.Println("Month over month stats\n=====================================")
	for i := len(keys) - 1; i >= 0; i-- {
		k := keys[i]
		fmt.Println(k, fmt.Sprintf("New subscriptions: %d", months[k].subscriptions), fmt.Sprintf("MRR: %.2f", months[k].revenue))
	}

	sort.Slice(subscriptions, func(i, j int) bool {
		return subscriptions[i].startDate.After(subscriptions[j].startDate)
	})

	fmt.Printf("MRR Last Day : %.1f\n", getMrrLastPeriod(time.Now().Add(-time.Hour*24), subscriptions))
	fmt.Printf("MRR Last 7 days : %.1f\n", getMrrLastPeriod(time.Now().Add(-time.Hour*24*7), subscriptions))
	fmt.Printf("MRR Last 30 days : %.1f\n", getMrrLastPeriod(time.Now().Add(-time.Hour*24*30), subscriptions))
	fmt.Printf("MRR Last 90 days : %.1f\n", getMrrLastPeriod(time.Now().Add(-time.Hour*24*90), subscriptions))
}

// getMrrLastPeriod return the MRR since period
// subscriptions must be sorted asc by startDate
func getMrrLastPeriod(period time.Time, subscriptions []subscriptionStat) float64 {
	periodMrr := 0.0
	periodIndex := sort.Search(len(subscriptions), func(i int) bool {
		return period.After(subscriptions[i].startDate)
	})
	periodSubs := subscriptions[:periodIndex]
	for _, s := range periodSubs {
		periodMrr += s.revenue
	}
	return periodMrr
}

//func getSubcriptionsBefore(subscriptions []subscriptionStat, allBefore time.Time){
//	index := sort.Search(len(subscriptions), func(i int) bool {
//		return subscriptions[i].startDate.Before(time.Now().Add(-time.Hour * 24 * 7 * 30))
//	})
//}

func applyDiscount(v float64, discount *stripe.Discount) float64 {
	if discount == nil || discount.Coupon == nil {
		return v
	}

	// If the discount end in less than a year, we don't take it into account for the mrr
	now := time.Now()
	nextYear := time.Date(now.Year()+1, now.Month(), 0, 0, 0, 0, 0, now.Location())
	discountEnd := time.Unix(discount.End, 0)
	if discountEnd.Before(nextYear) {
		return v
	}

	coupon := discount.Coupon
	if coupon.AmountOff > 0 {
		return v - float64(coupon.AmountOff)
	} else if coupon.PercentOff > 0 {
		return v - ((float64(coupon.PercentOff) / 100.0) * v)
	}
	return v
}
